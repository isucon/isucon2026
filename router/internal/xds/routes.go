package xds

import (
	"errors"
	"fmt"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	faultcommonv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/common/fault/v3"
	bandwidthv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/bandwidth_limit/v3"
	faultv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/fault/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/isucon/isucon2026/router/internal/config"
	"github.com/isucon/isucon2026/router/internal/domain"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	faultFilterName     = "envoy.filters.http.fault"
	bandwidthFilterName = "envoy.filters.http.bandwidth_limit"
)

func BuildRoutes(name string, hosts config.Hostnames, routerConfig domain.Config, profile domain.TrafficProfile) (*routev3.RouteConfiguration, error) {
	if name == "" {
		return nil, errors.New("route config name is required")
	}
	if err := routerConfig.Validate(); err != nil {
		return nil, fmt.Errorf("router config: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return nil, fmt.Errorf("traffic profile: %w", err)
	}
	hostValues := []string{hosts.App, hosts.App1, hosts.App2, hosts.App3}
	seen := make(map[string]struct{}, len(hostValues))
	for _, host := range hostValues {
		if host == "" {
			return nil, errors.New("all hostnames are required")
		}
		if _, ok := seen[host]; ok {
			return nil, fmt.Errorf("duplicate hostname %q", host)
		}
		seen[host] = struct{}{}
	}

	targets := []struct {
		name   string
		host   string
		server domain.ServerID
	}{
		{name: "app", host: hosts.App},
		{name: string(domain.App1), host: hosts.App1, server: domain.App1},
		{name: string(domain.App2), host: hosts.App2, server: domain.App2},
		{name: string(domain.App3), host: hosts.App3, server: domain.App3},
	}

	virtualHosts := make([]*routev3.VirtualHost, 0, len(targets)+1)
	for _, target := range targets {
		routes := make([]*routev3.Route, 0, len(domain.Countries())+2)
		for _, country := range domain.Countries() {
			countryRegion, _ := domain.CountryRegion(country)
			server := target.server
			if server == "" {
				server = domain.ServerForRegion(countryRegion)
			}
			route, err := buildUpstreamRoute(target.name+"-"+country, server, routerConfig.RegionFor(server), countryRegion, profile, []*routev3.HeaderMatcher{{
				Name: "x-country-code",
				HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{StringMatch: &matcherv3.StringMatcher{
					MatchPattern: &matcherv3.StringMatcher_Exact{Exact: country},
				}},
			}}, "")
			if err != nil {
				return nil, err
			}
			routes = append(routes, route)
		}
		defaultRegion, _ := domain.CountryRegion(routerConfig.DefaultCountry)
		server := target.server
		if server == "" {
			server = domain.ServerForRegion(defaultRegion)
		}
		defaultRoute, err := buildUpstreamRoute(target.name+"-default", server, routerConfig.RegionFor(server), defaultRegion, profile, []*routev3.HeaderMatcher{{
			Name:                 "x-country-code",
			HeaderMatchSpecifier: &routev3.HeaderMatcher_PresentMatch{PresentMatch: true},
			InvertMatch:          true,
		}}, routerConfig.DefaultCountry)
		if err != nil {
			return nil, err
		}
		routes = append(routes, defaultRoute, directResponseRoute(target.name+"-invalid-country", 400))
		virtualHosts = append(virtualHosts, &routev3.VirtualHost{Name: target.name, Domains: []string{target.host}, Routes: routes})
	}
	virtualHosts = append(virtualHosts, &routev3.VirtualHost{
		Name: "unknown-host", Domains: []string{"*"}, Routes: []*routev3.Route{directResponseRoute("unknown-host", 404)},
	})
	return &routev3.RouteConfiguration{
		Name: name, VirtualHosts: virtualHosts, ValidateClusters: wrapperspb.Bool(true), IgnorePortInHostMatching: true,
	}, nil
}

func buildUpstreamRoute(name string, server domain.ServerID, serverRegion, clientRegion domain.Region, profile domain.TrafficProfile, headers []*routev3.HeaderMatcher, defaultCountry string) (*routev3.Route, error) {
	class := profile.Class(clientRegion, serverRegion)
	bandwidth := &bandwidthv3.BandwidthLimit{
		StatPrefix:   name,
		EnableMode:   bandwidthv3.BandwidthLimit_REQUEST_AND_RESPONSE,
		LimitKbps:    wrapperspb.UInt64((class.BandwidthBPS + 8191) / 8192),
		FillInterval: durationpb.New(50 * time.Millisecond),
	}
	bandwidthAny, err := anypb.New(bandwidth)
	if err != nil {
		return nil, fmt.Errorf("marshal bandwidth config: %w", err)
	}
	typedConfigs := map[string]*anypb.Any{bandwidthFilterName: bandwidthAny}
	if class.Delay > 0 {
		fault := &faultv3.HTTPFault{Delay: &faultcommonv3.FaultDelay{
			FaultDelaySecifier: &faultcommonv3.FaultDelay_FixedDelay{FixedDelay: durationpb.New(class.Delay)},
			Percentage:         &typev3.FractionalPercent{Numerator: 100, Denominator: typev3.FractionalPercent_HUNDRED},
		}}
		faultAny, err := anypb.New(fault)
		if err != nil {
			return nil, fmt.Errorf("marshal fault config: %w", err)
		}
		typedConfigs[faultFilterName] = faultAny
	}
	route := &routev3.Route{
		Name: name,
		Match: &routev3.RouteMatch{
			PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"},
			Headers:       headers,
		},
		Action: &routev3.Route_Route{Route: &routev3.RouteAction{
			ClusterSpecifier: &routev3.RouteAction_Cluster{Cluster: string(server)},
			Timeout:          durationpb.New(30 * time.Second),
		}},
		TypedPerFilterConfig: typedConfigs,
	}
	if defaultCountry != "" {
		route.RequestHeadersToAdd = append(route.RequestHeadersToAdd, &corev3.HeaderValueOption{
			Header:       &corev3.HeaderValue{Key: "x-country-code", Value: defaultCountry},
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
		})
	}
	route.RequestHeadersToAdd = append(route.RequestHeadersToAdd, &corev3.HeaderValueOption{
		Header:       &corev3.HeaderValue{Key: "x-client-ip", Value: "%DOWNSTREAM_DIRECT_REMOTE_ADDRESS_WITHOUT_PORT%"},
		AppendAction: corev3.HeaderValueOption_ADD_IF_ABSENT,
	})
	return route, nil
}

func directResponseRoute(name string, status uint32) *routev3.Route {
	return &routev3.Route{
		Name:   name,
		Match:  &routev3.RouteMatch{PathSpecifier: &routev3.RouteMatch_Prefix{Prefix: "/"}},
		Action: &routev3.Route_DirectResponse{DirectResponse: &routev3.DirectResponseAction{Status: status}},
	}
}
