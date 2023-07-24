package mempool

import (
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	newDailyTxCount    = metrics.NewRegisteredGauge("mempool/addtx", nil)
	newDailyAllTxCount = metrics.NewRegisteredGauge("mempool/alltx", nil)
)
