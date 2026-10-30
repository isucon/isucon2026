package external

import (
	"net"
	"testing"
)

func TestRouterAddressRequiresPrivateIPv4(t *testing.T) {
	for _, address := range []string{"203.0.113.5", "", "router.example", "127.0.0.1", "::1"} {
		if validRouterAddress(address) {
			t.Fatalf("accepted %s", address)
		}
	}
	if !validRouterAddress(net.ParseIP("192.168.10.10").String()) {
		t.Fatal("rejected data ENI")
	}
}
