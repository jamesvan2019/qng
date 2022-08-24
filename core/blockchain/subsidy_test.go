package blockchain

import (
	"github.com/Qitmeer/qng/consensus/forks"
	"github.com/Qitmeer/qng/meerdag"
	"github.com/Qitmeer/qng/params"
	"testing"
)

// TestEstimateSupply ensures the supply estimation function defined by MeerEVM-fork works as expected.
func TestEstimateSupplyByMeerEVMFork(t *testing.T) {
	param := params.MainNetParam.Params
	baseSubsidy := param.BaseSubsidy
	endBlockHeight := int64(62621828)
	expectTotalSubsidy := int64(forks.MeerEVMForkTotalSubsidy)
	totalSubsidy := int64(0)
	bis := map[int64]*meerdag.BlueInfo{}
	subsidyCache := NewSubsidyCache(0, param)
	calcBlockSubsidy := func(height int64) int64 {
		bi, ok := bis[height]
		if !ok {
			t.Fatalf("No bi:%d", height)
		}
		return subsidyCache.CalcBlockSubsidy(bi)
	}

	for i := int64(0); i <= endBlockHeight; i++ {
		var weight int64
		if i > 0 {
			pheight := i - 1
			pbi, ok := bis[pheight]
			if !ok {
				t.Fatal("No test bi")
			}
			weight = pbi.GetWeight() + calcBlockSubsidy(pheight)
		}
		bis[i] = meerdag.NewBlueInfo(uint(i), 0, weight, i)
		totalSubsidy += weight
	}

	blockOneSubsidy := calcBlockSubsidy(1)
	blockTwoSubsidy := calcBlockSubsidy(2)

	tests := []struct {
		height        int64
		expectSubsidy int64
		expectMode    string
	}{
		{height: 0, expectSubsidy: baseSubsidy},
		{height: 1, expectSubsidy: blockOneSubsidy},
		{height: 2, expectSubsidy: blockTwoSubsidy},
		{height: forks.MeerEVMForkMainHeight, expectMode: "meerevmfork"},
	}

	for _, test := range tests {
		if test.expectSubsidy > 0 {
			gotSupply := calcBlockSubsidy(test.height)
			if gotSupply != test.expectSubsidy {
				t.Fatalf("calcBlockSubsidy (height %d): did not get "+
					"expected supply - got %d, want %d", test.height,
					gotSupply, test.expectSubsidy)
			}
		}
		if len(test.expectMode) > 0 {
			cm := subsidyCache.GetMode(test.height)
			if cm != test.expectMode {
				t.Fatalf("subsidy mode is %s ,want %s", cm, test.expectMode)
			}
		}
	}

	if totalSubsidy < expectTotalSubsidy {
		t.Fatalf("total subsidy:%d ,want: %d", totalSubsidy, expectTotalSubsidy)
	}

	endBaseSubsidy := calcBlockSubsidy(endBlockHeight)
	if endBaseSubsidy != 0 {
		t.Fatalf("Base subsidy is not zero:%d", endBaseSubsidy)
	}
}
