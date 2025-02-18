// Copyright (c) 2017-2018 The qitmeer developers
// Copyright (c) 2013-2016 The btcsuite developers
// Copyright (c) 2015-2016 The Decred developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"fmt"
	"github.com/Qitmeer/qng/consensus/forks"
	"github.com/Qitmeer/qng/consensus/vm"
	"github.com/Qitmeer/qng/core/blockchain/utxo"
	"math"
	"runtime"

	"github.com/Qitmeer/qng/core/types"
	"github.com/Qitmeer/qng/engine/txscript"
)

// txValidateItem holds a transaction along with which input to validate.
type txValidateItem struct {
	txInIndex int
	txIn      *types.TxInput
	tx        *types.Tx
}

// txValidator provides a type which asynchronously validates transaction
// inputs.  It provides several channels for communication and a processing
// function that is intended to be in run multiple goroutines.
type txValidator struct {
	validateChan chan *txValidateItem
	quitChan     chan struct{}
	resultChan   chan error
	utxoView     *utxo.UtxoViewpoint
	flags        txscript.ScriptFlags
	sigCache     *txscript.SigCache
}

// sendResult sends the result of a script pair validation on the internal
// result channel while respecting the quit channel.  The allows orderly
// shutdown when the validation process is aborted early due to a validation
// error in one of the other goroutines.
func (v *txValidator) sendResult(result error) {
	select {
	case v.resultChan <- result:
	case <-v.quitChan:
	}
}

// validateHandler consumes items to validate from the internal validate channel
// and returns the result of the validation on the internal result channel. It
// must be run as a goroutine.
func (v *txValidator) validateHandler() {
out:
	for {
		select {
		case txVI := <-v.validateChan:
			// Ensure the referenced input transaction is available.
			txIn := txVI.txIn
			utxo := v.utxoView.LookupEntry(txIn.PreviousOut)
			if utxo == nil {
				str := fmt.Sprintf("unable to find unspent "+
					"output %v referenced from "+
					"transaction %s:%d",
					txIn.PreviousOut, txVI.tx.Hash(),
					txVI.txInIndex)
				err := ruleError(ErrMissingTxOut, str)
				v.sendResult(err)
				break out
			}

			// Ensure the referenced input transaction public key
			// script is available.
			pkScript := utxo.PkScript()
			sigScript := txIn.SignScript
			vm, err := txscript.NewEngine(pkScript, txVI.tx.Transaction(),
				txVI.txInIndex, v.flags, txscript.DefaultScriptVersion, v.sigCache)
			if err != nil {
				str := fmt.Sprintf("failed to parse input "+
					"%s:%d which references output %v - "+
					"%v (input script "+
					"bytes %x, prev output script bytes %x)",
					txVI.tx.Hash(), txVI.txInIndex,
					txIn.PreviousOut, err,
					sigScript, pkScript)
				err := ruleError(ErrScriptMalformed, str)
				v.sendResult(err)
				break out
			}

			// Execute the script pair.
			if err := vm.Execute(); err != nil {
				str := fmt.Sprintf("failed to validate input "+
					"%s:%d which references output %v - "+
					"%v (input script "+
					"bytes %x, prev output script bytes %x)",
					txVI.tx.Hash(), txVI.txInIndex,
					txIn.PreviousOut, err,
					sigScript, pkScript)
				err := ruleError(ErrScriptValidation, str)
				v.sendResult(err)
				break out
			}

			// Validation succeeded.
			v.sendResult(nil)

		case <-v.quitChan:
			break out
		}
	}
}

// Validate validates the scripts for all of the passed transaction inputs using
// multiple goroutines.
func (v *txValidator) Validate(items []*txValidateItem) error {
	if len(items) == 0 {
		return nil
	}

	// Limit the number of goroutines to do script validation based on the
	// number of processor cores.  This help ensure the system stays
	// reasonably responsive under heavy load.
	maxGoRoutines := runtime.NumCPU() * 3
	if maxGoRoutines <= 0 {
		maxGoRoutines = 1
	}
	if maxGoRoutines > len(items) {
		maxGoRoutines = len(items)
	}

	// Start up validation handlers that are used to asynchronously
	// validate each transaction input.
	for i := 0; i < maxGoRoutines; i++ {
		go v.validateHandler()
	}

	// Validate each of the inputs.  The quit channel is closed when any
	// errors occur so all processing goroutines exit regardless of which
	// input had the validation error.
	numInputs := len(items)
	currentItem := 0
	processedItems := 0
	for processedItems < numInputs {
		// Only send items while there are still items that need to
		// be processed.  The select statement will never select a nil
		// channel.
		var validateChan chan *txValidateItem
		var item *txValidateItem
		if currentItem < numInputs {
			validateChan = v.validateChan
			item = items[currentItem]
		}

		select {
		case validateChan <- item:
			currentItem++

		case err := <-v.resultChan:
			processedItems++
			if err != nil {
				close(v.quitChan)
				return err
			}
		}
	}

	close(v.quitChan)
	return nil
}

// newTxValidator returns a new instance of txValidator to be used for
// validating transaction scripts asynchronously.
func newTxValidator(utxoView *utxo.UtxoViewpoint, flags txscript.ScriptFlags, sigCache *txscript.SigCache) *txValidator {
	return &txValidator{
		validateChan: make(chan *txValidateItem),
		quitChan:     make(chan struct{}),
		resultChan:   make(chan error),
		utxoView:     utxoView,
		sigCache:     sigCache,
		flags:        flags,
	}
}

// ValidateTransactionScripts validates the scripts for the passed transaction
// using multiple goroutines.
func ValidateTransactionScripts(tx *types.Tx, utxoView *utxo.UtxoViewpoint, flags txscript.ScriptFlags, sigCache *txscript.SigCache, height int64) error {
	// Collect all of the transaction inputs and required information for
	// validation.
	txIns := tx.Transaction().TxIn
	txValItems := make([]*txValidateItem, 0, len(txIns))
	for txInIdx, txIn := range txIns {
		// Skip coinbases.
		if txIn.PreviousOut.OutIndex == math.MaxUint32 {
			continue
		}
		if forks.IsVaildEVMUTXOUnlockTx(tx.Tx, txIn, height) {
			txIn.AmountIn.Value = types.MeerEVMForkInput
		}
		txVI := &txValidateItem{
			txInIndex: txInIdx,
			txIn:      txIn,
			tx:        tx,
		}
		txValItems = append(txValItems, txVI)
	}

	// Validate all of the inputs.
	return newTxValidator(utxoView, flags, sigCache).Validate(txValItems)

}

// checkBlockScripts executes and validates the scripts for all transactions in
// the passed block using multiple goroutines.
// txTree = true is TxTreeRegular, txTree = false is TxTreeStake.
func (b *BlockChain) checkBlockScripts(block *types.SerializedBlock, utxoView *utxo.UtxoViewpoint,
	scriptFlags txscript.ScriptFlags, sigCache *txscript.SigCache) error {

	// Collect all of the transaction inputs and required information for
	// validation for all transactions in the block into a single slice.
	numInputs := 0
	txs := block.Transactions()
	for _, tx := range txs {
		if tx.IsDuplicate || types.IsCrossChainVMTx(tx.Tx) {
			continue
		}
		numInputs += len(tx.Transaction().TxIn)
	}
	txValItems := make([]*txValidateItem, 0, numInputs)
	for _, tx := range txs {
		if tx.IsDuplicate || types.IsCrossChainVMTx(tx.Tx) {
			continue
		}
		if types.IsCrossChainImportTx(tx.Tx) {
			itx, err := vm.NewImportTx(tx.Tx)
			if err != nil {
				return err
			}
			pks, err := itx.GetPKScript()
			if err != nil {
				return err
			}
			utxoView.AddTokenTxOut(tx.Tx.TxIn[0].PreviousOut, pks)
			vtsTx, err := itx.GetTransactionForEngine()
			if err != nil {
				return err
			}
			txVI := &txValidateItem{
				txInIndex: 0,
				txIn:      vtsTx.TxIn[0],
				tx:        types.NewTx(vtsTx),
			}
			txValItems = append(txValItems, txVI)
			continue
		}
		for txInIdx, txIn := range tx.Transaction().TxIn {
			// Skip coinbases.
			if txIn.PreviousOut.OutIndex == math.MaxUint32 {
				continue
			}
			if forks.IsVaildEVMUTXOUnlockTx(tx.Tx, txIn, int64(block.Height())) {
				txIn.AmountIn.Value = types.MeerEVMForkInput
			}
			txVI := &txValidateItem{
				txInIndex: txInIdx,
				txIn:      txIn,
				tx:        tx,
			}
			txValItems = append(txValItems, txVI)
		}

		if types.IsTokenTx(tx.Tx) {
			if types.IsTokenMintTx(tx.Tx) {
				state := b.GetTokenState(b.TokenTipID)
				if state == nil {
					return fmt.Errorf("Token state error\n")
				}
				tt, ok := state.Types[tx.Tx.TxOut[0].Amount.Id]
				if !ok {
					return fmt.Errorf("It doesn't exist: Coin id (%d)\n", tx.Tx.TxOut[0].Amount.Id)
				}
				utxoView.AddTokenTxOut(tx.Tx.TxIn[0].PreviousOut, tt.Owners)
			} else {
				utxoView.AddTokenTxOut(tx.Tx.TxIn[0].PreviousOut, nil)
			}
		}
	}

	// Validate all of the inputs.
	return newTxValidator(utxoView, scriptFlags, sigCache).Validate(txValItems)
}
