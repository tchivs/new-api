package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPickDeepSeekBalance(t *testing.T) {
	infos := func(currencies ...string) []DeepSeekBalanceInfo {
		out := make([]DeepSeekBalanceInfo, 0, len(currencies))
		for _, currency := range currencies {
			out = append(out, DeepSeekBalanceInfo{Currency: currency, TotalBalance: "1"})
		}
		return out
	}

	tests := []struct {
		name  string
		infos []DeepSeekBalanceInfo
		want  int
	}{
		{name: "domestic account reports CNY", infos: infos("CNY"), want: 0},
		{name: "international account reports USD", infos: infos("USD"), want: 0},
		{name: "CNY wins over USD", infos: infos("USD", "CNY"), want: 1},
		{name: "unknown single currency still usable", infos: infos("HKD"), want: 0},
		{name: "no entries", infos: infos(), want: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pickDeepSeekBalance(tt.infos))
		})
	}
}
