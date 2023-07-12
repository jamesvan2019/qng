package mempool

import (
	"github.com/ethereum/go-ethereum/metrics"
)

var (
	newTxCount = metrics.NewRegisteredGauge("mempool/addtx", nil)
)
