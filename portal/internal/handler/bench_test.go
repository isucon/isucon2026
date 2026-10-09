package handler

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"github.com/isucon/isucon2026/portal/internal/model"
)

func TestInstancePublicAddresses(t *testing.T) {
	for _, count := range []int{1, 3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			var instances []*model.ContestantInstance
			var want []string
			for n := 1; n <= count; n++ {
				address := fmt.Sprintf("203.0.113.%d", n)
				instances = append(instances, &model.ContestantInstance{Number: int64(n), PublicIPv4Address: sql.NullString{String: address, Valid: true}})
				want = append(want, address)
			}
			if got := instancePublicAddresses(instances); !slices.Equal(got, want) {
				t.Fatalf("addresses = %v, want %v", got, want)
			}
		})
	}
}
