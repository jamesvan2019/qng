package wallet

import (
	"encoding/hex"
	"fmt"
	"github.com/Qitmeer/qng/core/address"
	"github.com/Qitmeer/qng/core/json"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"time"
)

func (api *PublicWalletManagerAPI) Unlock(account, passphrase string, timeout time.Duration) error {
	a, err := utils.MakeAddress(api.a.qks.KeyStore, account)
	if err != nil {
		return err
	}
	_, key, err := api.a.qks.getDecryptedKey(a, passphrase)
	if err != nil {
		return err
	}

	api.a.qks.mu.Lock()
	defer api.a.qks.mu.Unlock()
	addrs, err := GetQngAddrsFromPrivateKey(hex.EncodeToString(key.PrivateKey.D.Bytes()), api.a.am.GetChain().ChainParams())
	if err != nil {
		return err
	}
	for _, addr := range addrs {
		_ = api.a.am.AddAddress(addr.String())

		u, found := api.a.qks.unlocked[addr]
		if found {
			if u.abort == nil {
				// The address was unlocked indefinitely, so unlocking
				// it with a timeout would be confusing.
				zeroKey(key.PrivateKey)
				return nil
			}
			// Terminate the expire goroutine and replace it below.
			close(u.abort)
		}
		if timeout > 0 {
			u = &unlocked{Key: key, abort: make(chan struct{})}
			go api.a.qks.expire(addr, u, timeout)
		} else {
			u = &unlocked{Key: key}
		}
		api.a.qks.unlocked[addr] = u
	}
	return nil
}

// Lock removes the private key with the given address from memory.
func (api *PublicWalletManagerAPI) Lock(addres string) error {
	addr, err := address.DecodeAddress(addres)
	if err != nil {
		return err
	}
	api.a.qks.mu.Lock()
	if unl, found := api.a.qks.unlocked[addr]; found {
		api.a.qks.mu.Unlock()
		api.a.qks.expire(addr, unl, time.Duration(0)*time.Nanosecond)
	} else {
		api.a.qks.mu.Unlock()
	}

	return nil
}

//SendToAddress handles a sendtoaddress RPC request by creating a new
//transaction spending unspent transaction outputs for a wallet to another
//payment address.  Leftover inputs not sent to the payment address or a fee
//for the miner are sent back to a new address in the wallet.  Upon success,
//the TxID for the created transaction is returned.
func (api *PublicWalletManagerAPI) SendToAddress(fromAddress string, amounts json.AddressAmountV3, lockTime int64) (string, error) {

	for _, a := range amounts {
		// Check that signed integer parameters are positive.
		if a.Amount <= 0 {
			return "", fmt.Errorf("amount must be positive")
		}
	}

	return api.a.sendTx(fromAddress, amounts, 0, lockTime)
}