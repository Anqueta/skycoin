package wallet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/go-bip39"
	"github.com/skycoin/skycoin/src/coin"
	"github.com/skycoin/skycoin/src/visor/blockdb"
)

// ErrWalletNotExist is returned if a wallet does not exist
var ErrWalletNotExist = errors.New("wallet doesn't exist")
var ErrWalletApiDisabled = errors.New("wallet api disabled")

// BalanceGetter interface for getting the balance of given addresses
type BalanceGetter interface {
	GetBalanceOfAddrs(addrs []cipher.Address) ([]BalancePair, error)
}

// Service wallet service struct
type Service struct {
	sync.RWMutex
	wallets          Wallets
	firstAddrIDMap   map[string]string // key: first address in wallet, value: wallet id
	disableWalletAPI bool
	WalletDirectory  string
}

// NewService new wallet service
func NewService(walletDir string, disableWalletAPI bool) (*Service, error) {
	serv := &Service{
		disableWalletAPI: disableWalletAPI,
		firstAddrIDMap:   make(map[string]string),
	}
	if serv.disableWalletAPI {
		return serv, nil
	}
	if err := os.MkdirAll(walletDir, os.FileMode(0700)); err != nil {
		return nil, fmt.Errorf("failed to create wallet directory %s: %v", walletDir, err)
	}

	serv.WalletDirectory = walletDir

	w, err := LoadWallets(serv.WalletDirectory)
	if err != nil {
		return nil, fmt.Errorf("failed to load all wallets: %v", err)
	}

	serv.wallets = serv.removeDup(w)

	if len(serv.wallets) == 0 {
		seed, err := bip39.NewDefaultMnemomic()
		if err != nil {
			return nil, err
		}

		// create default wallet
		w, err := serv.CreateWallet("", Options{
			Label: "Your Wallet",
			Seed:  seed,
		})
		if err != nil {
			return nil, err
		}

		if err := w.Save(serv.WalletDirectory); err != nil {
			return nil, fmt.Errorf("failed to save wallets to %s: %v", serv.WalletDirectory, err)
		}
	}

	return serv, nil
}

// CreateWallet creates a wallet with one address
func (serv *Service) CreateWallet(wltName string, options Options) (Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	if serv.disableWalletAPI {
		return Wallet{}, ErrWalletApiDisabled
	}
	if wltName == "" {
		wltName = serv.generateUniqueWalletFilename()
	}

	return serv.loadWallet(wltName, options, 0, nil)
}

// ScanAheadWalletAddresses scans n addresses for a balance, and sets the wallet's entry list to the highest
// address with a non-zero coins balance.
func (serv *Service) ScanAheadWalletAddresses(wltName string, scanN uint64, bg BalanceGetter) (Wallet, error) {
	serv.Lock()
	defer serv.Unlock()

	w, err := serv.getWallet(wltName)
	if err != nil {
		return Wallet{}, err
	}

	if err := w.ScanAddresses(scanN, bg); err != nil {
		return Wallet{}, err
	}

	if err := Save(serv.WalletDirectory); err != nil {
		return Wallet{}, err
	}

	serv.wallets.set(w)

	return w.Copy(), nil
}

// loadWallet loads wallet from seed and scan the first N addresses
func (serv *Service) loadWallet(wltName string, options Options, scanN uint64, bg BalanceGetter) (Wallet, error) {
	w, err := NewWallet(wltName, options)
	if err != nil {
		return Wallet{}, err
	}

	// Generate a default address
	w.GenerateAddresses(1)

	// Check for duplicate wallets by initial seed
	if id, ok := serv.firstAddrIDMap[w.Entries[0].Address.String()]; ok {
		return Wallet{}, fmt.Errorf("duplicate wallet with %v", id)
	}

	// Scan for addresses with balances
	if scanN > 1 && bg != nil {
		if err := w.ScanAddresses(scanN-1, bg); err != nil {
			return Wallet{}, err
		}
	}

	if err := serv.wallets.Add(*w); err != nil {
		return Wallet{}, err
	}

	if err := w.Save(serv.WalletDirectory); err != nil {
		// If save fails, remove the added wallet
		serv.wallets.Remove(w.GetID())
		return Wallet{}, err
	}

	serv.firstAddrIDMap[w.Entries[0].Address.String()] = w.Filename()

	return w.Copy(), nil
}

func (serv *Service) generateUniqueWalletFilename() string {
	wltName := NewWalletFilename()
	for {
		if _, ok := serv.wallets.Get(wltName); !ok {
			break
		}
		wltName = NewWalletFilename()
	}

	return wltName
}

// Encrypt encrypts wallet by given password
func (serv *Service) Encrypt(wltID, password string) (*Wallet, error) {
	serv.Lock()
	defer serv.Unlock()
	w, ok := serv.wallets.Get(wltID)
	if !ok {
		return nil, ErrWalletNotExist{wltID}
	}

	oldVersion := w.Version()

	// Update version to lastest
	w.setVersion(Version)

	// encrypt seed
	sseed, err := Encrypt([]byte(w.seed()), []byte(password))
	if err != nil {
		return nil, err
	}
	w.setSeed(sseed)

	// encrypt lastSeed
	lsseed, err := Encrypt([]byte(w.lastSeed()), []byte(password))
	if err != nil {
		return nil, err
	}
	w.setLastSeed(lsseed)

	// encrypts private keys in entries
	for i := range w.Entries {
		sk, err := Encrypt(w.Entries[i].Secret[:], []byte(password))
		if err != nil {
			return nil, err
		}

		w.Entries[i].EncryptedSeckey = sk
		w.Entries[i].Secret = cipher.SecKey{}
	}

	if err := Save(serv.WalletDirectory, w); err != nil {
		return nil, err
	}

	// Delete the .bak file if the previous version is 0.1,
	// othewise it would expose the plaintext seeds and private keys.
	if oldVersion == "0.1" {
		fn := w.Filename() + ".bak"
		path := filepath.Join(serv.WalletDirectory, fn)
		if e, err := os.Stat(path); !os.IsNotExist(err) {
			if !e.IsDir() {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
			}
		}
	}

	nw := w.clone()

	return nw, nil
}

// NewAddresses generate address entries in given wallet,
// return nil if wallet does not exist.
func (serv *Service) NewAddresses(wltID string, num uint64) ([]cipher.Address, error) {
	serv.Lock()
	defer serv.Unlock()
	w, ok := serv.wallets.Get(wltID)
	if !ok {
		return []cipher.Address{}, ErrWalletNotExist{wltID}
	}

	addrs, err := w.GenerateAddresses(num)
	if err != nil {
		return nil, err
	}

	if err := Save(w, serv.WalletDirectory); err != nil {
		return nil, err
	}

	return addrs, nil
}

// GetAddresses returns all addresses in given wallet
func (serv *Service) GetAddresses(wltID string) ([]cipher.Address, error) {
	serv.RLock()
	defer serv.RUnlock()
	w, ok := serv.wallets.Get(wltID)
	if !ok {
		return []cipher.Address{}, ErrWalletNotExist{wltID}
	}

	return w.GetAddresses(), nil
}

// GetWallet returns wallet by id
func (serv *Service) GetWallet(wltID string) (Wallet, error) {
	serv.RLock()
	defer serv.RUnlock()

	return serv.getWallet(wltID)
}

func (serv *Service) getWallet(wltID string) (Wallet, error) {
	w, ok := serv.wallets.Get(wltID)
	if !ok {
		return Wallet{}, ErrWalletNotExist
	}
	return w.clone(), nil
}

// GetWallets returns all wallets
func (serv *Service) GetWallets() Wallets {
	serv.RLock()
	defer serv.RUnlock()
	wlts := make(Wallets, len(serv.wallets))
	for k, w := range serv.wallets {
		nw := w.clone()
		wlts[k] = nw
	}
	return wlts
}

// ReloadWallets reload wallets
func (serv *Service) ReloadWallets() error {
	serv.Lock()
	defer serv.Unlock()
	if serv.disableWalletAPI {
		return ErrWalletApiDisabled
	}
	wallets, err := LoadWallets(serv.WalletDirectory)
	if err != nil {
		return err
	}

	serv.firstAddrIDMap = make(map[string]string)
	serv.wallets = serv.removeDup(wallets)
	return nil
}

// CreateAndSignTransaction creates and sign transaction from wallet
func (serv *Service) CreateAndSignTransaction(wltID string, vld Validator, unspent blockdb.UnspentGetter,
	headTime, coins uint64, dest cipher.Address) (*coin.Transaction, error) {
	serv.RLock()
	defer serv.RUnlock()
	w, ok := serv.wallets.Get(wltID)
	if !ok {
		return nil, ErrWalletNotExist{wltID}
	}

	return w.CreateAndSignTransaction(vld, unspent, headTime, coins, dest)
}

// UpdateWalletLabel updates the wallet label
func (serv *Service) UpdateWalletLabel(wltID, label string) error {
	serv.Lock()
	defer serv.Unlock()
	var wlt *Wallet
	if err := serv.wallets.update(wltID, func(w *Wallet) *Wallet {
		w.setLabel(label)
		wlt = w
		return w
	}); err != nil {
		return err
	}

	return Save(serv.WalletDirectory, wlt)
}

// Remove removes wallet of given wallet id from the service
func (serv *Service) Remove(wltID string) {
	serv.Lock()
	defer serv.Unlock()
	serv.wallets.Remove(wltID)
}

func (serv *Service) removeDup(wlts Wallets) Wallets {
	var rmWltIDS []string
	// remove dup wallets
	for wltID, wlt := range wlts {
		if len(wlt.Entries) == 0 {
			// empty wallet
			rmWltIDS = append(rmWltIDS, wltID)
			continue
		}

		addr := wlt.Entries[0].Address.String()
		id, ok := serv.firstAddrIDMap[addr]
		if ok {
			// check whose entries number is bigger
			pw, _ := wlts.Get(id)
			if len(pw.Entries) >= len(wlt.Entries) {
				rmWltIDS = append(rmWltIDS, wltID)
				continue
			}

			// replace the old wallet with the new one
			// records the wallet id that need to remove
			rmWltIDS = append(rmWltIDS, id)
			// update wallet id
			serv.firstAddrIDMap[addr] = wltID
			continue
		}

		serv.firstAddrIDMap[addr] = wltID
	}

	// remove the duplicate and empty wallet
	for _, id := range rmWltIDS {
		wlts.Remove(id)
	}

	return wlts
}

// ErrWalletNotExist represents wallet doesnt exist error
type ErrWalletNotExist struct {
	id string
}

// Error returns the error message
func (ew ErrWalletNotExist) Error() string {
	return fmt.Sprintf("wallet %s doesn't exist", ew.id)
}
