// Copyright 2018 The Fractal Team Authors
// This file is part of the fractal project.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package accountmanager

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/log"
	"github.com/fractalplatform/fractal/asset"
	"github.com/fractalplatform/fractal/common"
	"github.com/fractalplatform/fractal/crypto"
	"github.com/fractalplatform/fractal/params"
	"github.com/fractalplatform/fractal/snapshot"
	"github.com/fractalplatform/fractal/state"
	"github.com/fractalplatform/fractal/types"
	"github.com/fractalplatform/fractal/utils/rlp"
)

var (
	acctInfoPrefix      = "acctInfo"
	accountNameIDPrefix = "accountNameId"
	counterPrefix       = "accountCounter"
)
var acctManagerName = "sysAccount"

//var sysName string = "fractal.account"

var counterID uint64 = 4096

type AuthorActionType uint64

const (
	AddAuthor AuthorActionType = iota
	UpdateAuthor
	DeleteAuthor
)

type AccountAction struct {
	AccountName common.Name `json:"accountName,omitempty"`
	Founder     common.Name `json:"founder,omitempty"`
	//ChargeRatio uint64        `json:"chargeRatio,omitempty"`
	PublicKey common.PubKey `json:"publicKey,omitempty"`
	Detail    string        `json:"detail,omitempty"`
}

type AuthorAction struct {
	ActionType AuthorActionType
	Author     *common.Author
}

type AccountAuthorAction struct {
	Threshold             uint64          `json:"threshold,omitempty"`
	UpdateAuthorThreshold uint64          `json:"updateAuthorThreshold,omitempty"`
	AuthorActions         []*AuthorAction `json:"authorActions,omitempty"`
}

type IncAsset struct {
	AssetId uint64      `json:"assetId,omitempty"`
	Amount  *big.Int    `json:"amount,omitempty"`
	To      common.Name `json:"account,omitempty"`
}

//AccountManager represents account management model.
type AccountManager struct {
	sdb *state.StateDB
	ast *asset.Asset
}

func SetAccountNameConfig(config *Config) bool {
	if config == nil {
		return false
	}

	if config.AccountNameLevel < 0 || config.AccountNameLength <= 8 {
		return false
	}

	if config.AccountNameLevel > 0 {
		if config.SubAccountNameLength < 1 {
			return false
		}
	}

	common.SetAccountNameCheckRule(config.AccountNameLevel, config.AccountNameLength, config.SubAccountNameLength)
	return true
}

//SetSysName set the global sys name
//func SetSysName(name common.Name) bool {
//	if common.IsValidAccountName(name.String()) {
//		sysName = name.String()
//		return true
//	}
//	return false
//}

// func SetChainName(name common.Name) bool {
// 	if common.IsValidAccountName(name.String()) {
// 		chainName = name.String()
// 		return true
// 	}
// 	return false
// }

//SetAcctMangerName  set the global account manager name
func SetAcctMangerName(name common.Name) bool {
	if common.IsValidAccountName(name.String()) {
		acctManagerName = name.String()
		return true
	}
	return false
}

//NewAccountManager create new account manager
func NewAccountManager(db *state.StateDB) (*AccountManager, error) {
	if db == nil {
		return nil, ErrNewAccountErr
	}
	if len(acctManagerName) == 0 {
		log.Error("NewAccountManager error", "name", ErrAccountManagerNotExist, acctManagerName)
		return nil, ErrAccountManagerNotExist
	}
	am := &AccountManager{
		sdb: db,
		ast: asset.NewAsset(db),
	}

	am.InitAccountCounter()
	return am, nil
}

//InitAccountCounter init account manage counter
func (am *AccountManager) InitAccountCounter() {
	_, err := am.getAccountCounter()
	if err == ErrCounterNotExist {
		//var counterID uint64
		//counterID = 0
		//store assetCount
		b, err := rlp.EncodeToBytes(&counterID)
		if err != nil {
			panic(err)
		}
		am.sdb.Put(acctManagerName, counterPrefix, b)
	}
	return
}

//getAccountCounter get account counter cur value
func (am *AccountManager) getAccountCounter() (uint64, error) {
	b, err := am.sdb.Get(acctManagerName, counterPrefix)
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, ErrCounterNotExist
	}
	var accountCounter uint64
	err = rlp.DecodeBytes(b, &accountCounter)
	if err != nil {
		return 0, err
	}
	return accountCounter, nil
}

// AccountIsExist check account is exist.
func (am *AccountManager) AccountIsExist(accountName common.Name) (bool, error) {
	//check is exist
	accountID, err := am.GetAccountIDByName(accountName)
	if err != nil {
		return false, err
	}
	if accountID > 0 {
		return true, nil
	} else {
		return false, nil
	}
}

// AccountIDIsExist check account is exist by ID.
func (am *AccountManager) AccountIDIsExist(accountID uint64) (bool, error) {
	//check is exist
	account, err := am.GetAccountById(accountID)
	if err != nil {
		return false, err
	}
	if account != nil {
		return true, nil
	} else {
		return false, nil
	}
}

//AccountHaveCode check account have code
func (am *AccountManager) AccountHaveCode(accountName common.Name) (bool, error) {
	//check is exist
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return false, err
	}
	if acct == nil {
		return false, ErrAccountNotExist
	}

	return acct.HaveCode(), nil
}

//AccountIsEmpty check account is empty
func (am *AccountManager) AccountIsEmpty(accountName common.Name) (bool, error) {
	//check is exist
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return false, err
	}
	if acct == nil {
		return false, ErrAccountNotExist
	}

	if acct.IsEmpty() {
		return true, nil
	}
	return false, nil
}

//CreateAnyAccount include create sub account
func (am *AccountManager) CreateAnyAccount(fromName common.Name, accountName common.Name, founderName common.Name, number uint64, pubkey common.PubKey, detail string) error {

	if accountName.AccountNameLevel() > 1 {
		accountObj, err := am.GetAccountByName(fromName)
		if err != nil || accountObj == nil {
			return ErrAccountInvaid
		} else {
			if accountObj.GetAccountNumber() != 0 {
				return ErrAccountInvaid
			}
		}

		if !fromName.IsValidCreator(accountName.String()) {
			return ErrAccountInvaid
		}
	}

	if err := am.CreateAccount(accountName, founderName, number, pubkey, detail); err != nil {
		return err
	}

	return nil
}

//CreateAccount contract account
func (am *AccountManager) CreateAccount(accountName common.Name, founderName common.Name, number uint64, pubkey common.PubKey, detail string) error {
	if !common.IsValidAccountName(accountName.String()) {
		return fmt.Errorf("account %s is invalid", accountName.String())
	}

	//check is exist
	accountID, err := am.GetAccountIDByName(accountName)
	if err != nil {
		return err
	}
	if accountID > 0 {
		return ErrAccountIsExist
	}

	assetID, _ := am.ast.GetAssetIdByName(accountName.String())
	if assetID > 0 {
		return ErrNameIsExist
	}

	var fname common.Name
	if len(founderName.String()) > 0 && founderName != accountName {
		f, err := am.GetAccountByName(founderName)
		if err != nil {
			return err
		}
		if f == nil {
			return ErrAccountNotExist
		}
		fname.SetString(founderName.String())
	} else {
		fname.SetString(accountName.String())
	}

	acctObj, err := NewAccount(accountName, fname, pubkey, detail)
	if err != nil {
		return err
	}
	if acctObj == nil {
		return ErrCreateAccountError
	}

	//get accountCounter
	accountCounter, err := am.getAccountCounter()
	if err != nil {
		return err
	}
	accountCounter = accountCounter + 1
	//set account id
	acctObj.SetAccountID(accountCounter)

	//store account name with account id
	aid, err := rlp.EncodeToBytes(&accountCounter)
	if err != nil {
		return err
	}
	acctObj.SetAccountNumber(number)
	//acctObj.SetChargeRatio(0)
	am.SetAccount(acctObj)
	am.sdb.Put(acctManagerName, accountNameIDPrefix+accountName.String(), aid)
	am.sdb.Put(acctManagerName, counterPrefix, aid)
	return nil
}

//SetChargeRatio set the Charge Ratio of the account
// func (am *AccountManager) SetChargeRatio(accountName common.Name, ra uint64) error {
// 	acct, err := am.GetAccountByName(accountName)
// 	if acct == nil {
// 		return ErrAccountNotExist
// 	}
// 	if err != nil {
// 		return err
// 	}
// 	acct.SetChargeRatio(ra)
// 	return am.SetAccount(acct)
// }

//UpdateAccount update the pubkey of the account
func (am *AccountManager) UpdateAccount(accountName common.Name, accountAction *AccountAction) error {
	acct, err := am.GetAccountByName(accountName)
	if acct == nil {
		return ErrAccountNotExist
	}
	if err != nil {
		return err
	}
	if len(accountAction.Founder.String()) > 0 {
		f, err := am.GetAccountByName(accountAction.Founder)
		if err != nil {
			return err
		}
		if f == nil {
			return ErrAccountNotExist
		}
	} else {
		accountAction.Founder.SetString(accountName.String())
	}

	// if accountAction.ChargeRatio > 100 {
	// 	return ErrChargeRatioInvalid
	// }
	acct.SetFounder(accountAction.Founder)
	//acct.SetChargeRatio(accountAction.ChargeRatio)
	return am.SetAccount(acct)
}

func (am *AccountManager) UpdateAccountAuthor(accountName common.Name, acctAuth *AccountAuthorAction) error {
	acct, err := am.GetAccountByName(accountName)
	if acct == nil {
		return ErrAccountNotExist
	}
	if err != nil {
		return err
	}
	if acctAuth.Threshold != 0 {
		acct.SetThreshold(acctAuth.Threshold)
	}
	if acctAuth.UpdateAuthorThreshold != 0 {
		acct.SetUpdateAuthorThreshold(acctAuth.UpdateAuthorThreshold)
	}
	for _, authorAct := range acctAuth.AuthorActions {
		actionTy := authorAct.ActionType
		switch actionTy {
		case AddAuthor:
			acct.AddAuthor(authorAct.Author)
		case UpdateAuthor:
			acct.UpdateAuthor(authorAct.Author)
		case DeleteAuthor:
			acct.DeleteAuthor(authorAct.Author)
		default:
			return fmt.Errorf("invalid account author operation type %d", actionTy)
		}
	}
	acct.SetAuthorVersion()
	return am.SetAccount(acct)
}

//GetAccountByTime get account by name and time
func (am *AccountManager) GetAccountByTime(accountName common.Name, time uint64) (*Account, error) {
	accountID, err := am.GetAccountIDByName(accountName)
	if err != nil {
		return nil, err
	}

	snapshotManager := snapshot.NewSnapshotManager(am.sdb)
	b, err := snapshotManager.GetSnapshotMsg(acctManagerName, acctInfoPrefix+strconv.FormatUint(accountID, 10), time)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}

	var acct Account
	if err := rlp.DecodeBytes(b, &acct); err != nil {
		return nil, err
	}

	return &acct, nil
}

//GetAccountByName get account by name
func (am *AccountManager) GetAccountByName(accountName common.Name) (*Account, error) {
	accountID, err := am.GetAccountIDByName(accountName)
	if err != nil {
		return nil, err
	}
	return am.GetAccountById(accountID)
}

//GetAccountIDByName get account id by account name
func (am *AccountManager) GetAccountIDByName(accountName common.Name) (uint64, error) {
	if accountName == "" {
		return 0, nil
	}
	b, err := am.sdb.Get(acctManagerName, accountNameIDPrefix+accountName.String())
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, nil
	}
	var accountID uint64
	if err := rlp.DecodeBytes(b, &accountID); err != nil {
		return 0, err
	}
	return accountID, nil
}

//GetAccountById get account by account id
func (am *AccountManager) GetAccountById(id uint64) (*Account, error) {
	if id == 0 {
		return nil, nil
	}

	b, err := am.sdb.Get(acctManagerName, acctInfoPrefix+strconv.FormatUint(id, 10))

	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		log.Debug("account not exist", "id", ErrAccountNotExist, id)
		return nil, nil
	}
	var acct Account
	if err := rlp.DecodeBytes(b, &acct); err != nil {
		return nil, err
	}
	return &acct, nil
}

//SetAccount store account object to db
func (am *AccountManager) SetAccount(acct *Account) error {
	if acct == nil {
		return ErrAccountIsNil
	}
	if acct.IsDestroyed() == true {
		return ErrAccountIsDestroy
	}
	b, err := rlp.EncodeToBytes(acct)
	if err != nil {
		return err
	}
	//am.sdb.Put(acctManagerName, acctInfoPrefix+acct.GetName().String(), b)
	am.sdb.Put(acctManagerName, acctInfoPrefix+strconv.FormatUint(acct.GetAccountID(), 10), b)
	return nil
}

//DeleteAccountByName delete account
func (am *AccountManager) DeleteAccountByName(accountName common.Name) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return ErrAccountNotExist
	}
	if acct == nil {
		return ErrAccountNotExist
	}

	acct.SetDestroy()
	b, err := rlp.EncodeToBytes(acct)
	if err != nil {
		return err
	}
	am.sdb.Put(acct.GetName().String(), acctInfoPrefix, b)
	return nil
}

// GetNonce get nonce
func (am *AccountManager) GetNonce(accountName common.Name) (uint64, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return 0, err
	}
	if acct == nil {
		return 0, ErrAccountNotExist
	}
	return acct.GetNonce(), nil
}

// SetNonce set nonce
func (am *AccountManager) SetNonce(accountName common.Name, nonce uint64) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}
	acct.SetNonce(nonce)
	return am.SetAccount(acct)
}

// GetAuthorVersion returns the account author version
func (am *AccountManager) GetAuthorVersion(accountName common.Name) (common.Hash, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return common.Hash{}, err
	}
	if acct == nil {
		return common.Hash{}, ErrAccountNotExist
	}
	return acct.GetAuthorVersion(), nil
}

// RecoverTx Make sure the transaction is signed properly and validate account authorization.
func (am *AccountManager) RecoverTx(signer types.Signer, tx *types.Transaction) error {
	for _, action := range tx.GetActions() {
		pubs, err := types.RecoverMultiKey(signer, action, tx)
		if err != nil {
			return err
		}

		if uint64(len(pubs)) > params.MaxSignLength {
			return fmt.Errorf("exceed max sign length, want most %d, actual is %d", params.MaxSignLength, len(pubs))
		}

		recoverRes := &recoverActionResult{make(map[common.Name]*accountAuthor, 0)}
		for i, pub := range pubs {
			index := action.GetSignIndex(uint64(i))
			if uint64(len(index)) > params.MaxSignDepth {
				return fmt.Errorf("exceed max sign depth, want most %d, actual is %d", params.MaxSignDepth, len(index))
			}

			if err := am.ValidSign(action.Sender(), pub, index, recoverRes); err != nil {
				return err
			}
		}

		authorVersion := make(map[common.Name]common.Hash, 0)
		for name, acctAuthor := range recoverRes.acctAuthors {
			var count uint64
			for _, weight := range acctAuthor.indexWeight {
				count += weight
			}
			threshold := acctAuthor.threshold
			if name.String() == action.Sender().String() && action.Type() == types.UpdateAccountAuthor {
				threshold = acctAuthor.updateAuthorThreshold
			}
			if count < threshold {
				return fmt.Errorf("account %s want threshold %d, but actual is %d", name, acctAuthor.threshold, count)
			}
			authorVersion[name] = acctAuthor.version
		}

		types.StoreAuthorCache(action, authorVersion)
	}
	return nil
}

// IsValidSign
func (am *AccountManager) IsValidSign(accountName common.Name, pub common.PubKey) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}
	if acct.IsDestroyed() {
		return ErrAccountIsDestroy
	}
	//TODO action type verify

	for _, author := range acct.Authors {
		if author.String() == pub.String() && author.GetWeight() >= acct.GetThreshold() {
			return nil
		}
	}
	return fmt.Errorf("%v %v excepted %v", acct.AcctName, ErrkeyNotSame, pub.String())
}

//ValidSign check the sign
func (am *AccountManager) ValidSign(accountName common.Name, pub common.PubKey, index []uint64, recoverRes *recoverActionResult) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}
	if acct.IsDestroyed() {
		return ErrAccountIsDestroy
	}

	var i int
	var idx uint64
	for i, idx = range index {
		if idx >= uint64(len(acct.Authors)) {
			return fmt.Errorf("acct authors modified")
		}
		if i == len(index)-1 {
			break
		}
		switch ownerTy := acct.Authors[idx].Owner.(type) {
		case common.Name:
			nextacct, err := am.GetAccountByName(ownerTy)
			if err != nil {
				return err
			}
			if nextacct == nil {
				return ErrAccountNotExist
			}
			if nextacct.IsDestroyed() {
				return ErrAccountIsDestroy
			}
			if recoverRes.acctAuthors[acct.GetName()] == nil {
				a := &accountAuthor{version: acct.AuthorVersion, threshold: acct.Threshold, updateAuthorThreshold: acct.UpdateAuthorThreshold, indexWeight: map[uint64]uint64{idx: acct.Authors[idx].GetWeight()}}
				recoverRes.acctAuthors[acct.GetName()] = a
			} else {
				recoverRes.acctAuthors[acct.GetName()].indexWeight[idx] = acct.Authors[idx].GetWeight()
			}
			acct = nextacct
		default:
			return ErrAccountNotExist
		}
	}
	return am.ValidOneSign(acct, idx, pub, recoverRes)
}

func (am *AccountManager) ValidOneSign(acct *Account, index uint64, pub common.PubKey, recoverRes *recoverActionResult) error {
	switch ownerTy := acct.Authors[index].Owner.(type) {
	case common.PubKey:
		if pub.Compare(ownerTy) != 0 {
			return fmt.Errorf("%v %v have %v excepted %v", acct.AcctName, ErrkeyNotSame, pub.String(), ownerTy.String())
		}
	case common.Address:
		addr := common.BytesToAddress(crypto.Keccak256(pub.Bytes()[1:])[12:])
		if addr.Compare(ownerTy) != 0 {
			return fmt.Errorf("%v %v have %v excepted %v", acct.AcctName, ErrkeyNotSame, addr.String(), ownerTy.String())
		}
	default:
		return fmt.Errorf("wrong sign type")
	}
	if recoverRes.acctAuthors[acct.GetName()] == nil {
		a := &accountAuthor{version: acct.AuthorVersion, threshold: acct.Threshold, updateAuthorThreshold: acct.UpdateAuthorThreshold, indexWeight: map[uint64]uint64{index: acct.Authors[index].GetWeight()}}
		recoverRes.acctAuthors[acct.GetName()] = a
		return nil
	}
	recoverRes.acctAuthors[acct.GetName()].indexWeight[index] = acct.Authors[index].GetWeight()
	return nil
}

//GetAssetInfoByName get asset info by asset name.
func (am *AccountManager) GetAssetInfoByName(assetName string) (*asset.AssetObject, error) {
	assetID, err := am.ast.GetAssetIdByName(assetName)
	if err != nil {
		return nil, err
	}
	return am.ast.GetAssetObjectById(assetID)
}

//GetAssetInfoByID get asset info by assetID
func (am *AccountManager) GetAssetInfoByID(assetID uint64) (*asset.AssetObject, error) {
	return am.ast.GetAssetObjectById(assetID)
}

// GetAllAssetbyAssetId get accout asset and subAsset Info
func (am *AccountManager) GetAllAssetbyAssetId(acct *Account, assetId uint64) (map[uint64]*big.Int, error) {
	var ba = make(map[uint64]*big.Int, 0)

	b, err := acct.GetBalanceByID(assetId)
	if err != nil {
		return nil, err
	}
	ba[assetId] = b

	assetObj, err := am.ast.GetAssetObjectById(assetId)
	if err != nil {
		return nil, err
	}

	assetName := assetObj.GetAssetName()
	balances, err := acct.GetAllBalances()
	if err != nil {
		return nil, err
	}

	for id, balance := range balances {
		subAssetObj, err := am.ast.GetAssetObjectById(id)
		if err != nil {
			return nil, err
		}

		if common.IsValidCreator(assetName, subAssetObj.GetAssetName()) {
			ba[id] = balance
		}
	}

	return ba, nil
}

// GetAllBalancebyAssetID get account balance, balance(asset) = asset + subAsset
func (am *AccountManager) GetAllBalancebyAssetID(acct *Account, assetID uint64) (*big.Int, error) {
	var ba *big.Int
	ba = big.NewInt(0)

	b, _ := acct.GetBalanceByID(assetID)
	ba = ba.Add(ba, b)

	assetObj, err := am.ast.GetAssetObjectById(assetID)
	if err != nil {
		return big.NewInt(0), err
	}

	assetName := assetObj.GetAssetName()
	balances, err := acct.GetAllBalances()
	if err != nil {
		return big.NewInt(0), err
	}

	for id, balance := range balances {
		subAssetObj, err := am.ast.GetAssetObjectById(id)
		if err != nil {
			return big.NewInt(0), err
		}

		if common.IsValidCreator(assetName, subAssetObj.GetAssetName()) {
			ba = ba.Add(ba, balance)
		}
	}

	return ba, nil
}

//GetBalanceByTime get account balance by Time
func (am *AccountManager) GetBalanceByTime(accountName common.Name, assetID uint64, typeID uint64, time uint64) (*big.Int, error) {
	acct, err := am.GetAccountByTime(accountName, time)
	if err != nil {
		return big.NewInt(0), err
	}
	if acct == nil {
		return big.NewInt(0), ErrAccountNotExist
	}

	if typeID == 0 {
		return acct.GetBalanceByID(assetID)
	} else if typeID == 1 {
		return am.GetAllBalancebyAssetID(acct, assetID)
	} else {
		return big.NewInt(0), fmt.Errorf("type ID %d invalid", typeID)
	}
}

//GetAccountBalanceByID get account balance by ID
func (am *AccountManager) GetAccountBalanceByID(accountName common.Name, assetID uint64, typeID uint64) (*big.Int, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return big.NewInt(0), err
	}
	if acct == nil {
		return big.NewInt(0), ErrAccountNotExist
	}
	if typeID == 0 {
		return acct.GetBalanceByID(assetID)
	} else if typeID == 1 {
		return am.GetAllBalancebyAssetID(acct, assetID)
	} else {
		return big.NewInt(0), fmt.Errorf("type ID %d invalid", typeID)
	}
}

//GetAssetAmountByTime get asset amount by time
func (am *AccountManager) GetAssetAmountByTime(assetID uint64, time uint64) (*big.Int, error) {
	return am.ast.GetAssetAmountByTime(assetID, time)
}

//GetAccountLastChange account balance last change time
func (am *AccountManager) GetAccountLastChange(accountName common.Name) (uint64, error) {
	//TODO
	return 0, nil
}

//GetSnapshotTime get snapshot time
//num = 0  current snapshot time , 1 preview snapshot time , 2 next snapshot time
func (am *AccountManager) GetSnapshotTime(num uint64, time uint64) (uint64, error) {
	snapshotManager := snapshot.NewSnapshotManager(am.sdb)
	if num == 0 {
		return snapshotManager.GetLastSnapshotTime()
	} else if num == 1 {
		return snapshotManager.GetPrevSnapshotTime(time)
	} else if num == 2 {
		t, err := snapshotManager.GetLastSnapshotTime()
		if err != nil {
			return 0, err
		}

		if t <= time {
			return 0, ErrSnapshotTimeNotExist
		} else {
			for {
				if t1, err := snapshotManager.GetPrevSnapshotTime(t); err != nil {
					return t, nil
				} else if t1 <= time {
					return t, nil
				} else {
					t = t1
				}
			}
		}
	}
	return 0, ErrTimeTypeInvalid
}

//GetFounder Get Account Founder
func (am *AccountManager) GetFounder(accountName common.Name) (common.Name, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return "", err
	}
	if acct == nil {
		return "", ErrAccountNotExist
	}
	return acct.GetFounder(), nil
}

//GetAssetFounder Get Asset Founder
func (am *AccountManager) GetAssetFounder(assetID uint64) (common.Name, error) {
	return am.ast.GetAssetFounderById(assetID)
}

//GetChargeRatio Get Account ChargeRatio
// func (am *AccountManager) GetChargeRatio(accountName common.Name) (uint64, error) {
// 	acct, err := am.GetAccountByName(accountName)
// 	if err != nil {
// 		return 0, err
// 	}
// 	if acct == nil {
// 		return 0, ErrAccountNotExist
// 	}
// 	return acct.GetChargeRatio(), nil
// }

//GetAssetChargeRatio Get Asset ChargeRatio
// func (am *AccountManager) GetAssetChargeRatio(assetID uint64) (uint64, error) {
// 	acctName, err := am.ast.GetAssetFounderById(assetID)
// 	if err != nil {
// 		return 0, err
// 	}
// 	if acctName == "" {
// 		return 0, ErrAccountNotExist
// 	}
// 	return am.GetChargeRatio(acctName)
// }

//GetAccountBalanceByName get account balance by name
//func (am *AccountManager) GetAccountBalanceByName(accountName common.Name, assetName string) (*big.Int, error) {
//	acct, err := am.GetAccountByName(accountName)
//	if err != nil {
//		return big.NewInt(0), err
//	}
//	if acct == nil {
//		return big.NewInt(0), ErrAccountNotExist
//	}
//
//	assetID, err := am.ast.GetAssetIdByName(assetName)
//	if err != nil {
//		return big.NewInt(0), err
//	}
//	if assetID == 0 {
//		return big.NewInt(0), asset.ErrAssetNotExist
//	}
//
//	ba := &big.Int{}
//	ba, err = acct.GetBalanceByID(assetID)
//	if err != nil {
//		return big.NewInt(0), err
//	}
//
//	return ba, nil
//}

//SubAccountBalanceByID sub balance by assetID
func (am *AccountManager) SubAccountBalanceByID(accountName common.Name, assetID uint64, value *big.Int) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}

	if value.Cmp(big.NewInt(0)) < 0 {
		return ErrAmountValueInvalid
	}

	err = acct.SubBalanceByID(assetID, value)
	if err != nil {
		return err
	}

	return am.SetAccount(acct)
}

//AddAccountBalanceByID add balance by assetID
func (am *AccountManager) AddAccountBalanceByID(accountName common.Name, assetID uint64, value *big.Int) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}

	if value.Cmp(big.NewInt(0)) < 0 {
		return ErrAmountValueInvalid
	}

	err = acct.AddBalanceByID(assetID, value)
	if err != nil {
		return err
	}

	return am.SetAccount(acct)
}

//AddAccountBalanceByName  add balance by name
func (am *AccountManager) AddAccountBalanceByName(accountName common.Name, assetName string, value *big.Int) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}

	assetID, err := am.ast.GetAssetIdByName(assetName)
	if err != nil {
		return err
	}

	if assetID == 0 {
		return asset.ErrAssetNotExist
	}
	if value.Cmp(big.NewInt(0)) < 0 {
		return ErrAmountValueInvalid
	}

	err = acct.AddBalanceByID(assetID, value)
	if err != nil {
		return err
	}

	return am.SetAccount(acct)
}

//
func (am *AccountManager) EnoughAccountBalance(accountName common.Name, assetID uint64, value *big.Int) error {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}
	if value.Cmp(big.NewInt(0)) < 0 {
		return ErrAmountValueInvalid
	}
	return acct.EnoughAccountBalance(assetID, value)
}

//
func (am *AccountManager) GetCode(accountName common.Name) ([]byte, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return nil, err
	}
	if acct == nil {
		return nil, ErrAccountNotExist
	}
	return acct.GetCode()
}

////
//func (am *AccountManager) SetCode(accountName common.Name, code []byte) (bool, error) {
//	acct, err := am.GetAccountByName(accountName)
//	if err != nil {
//		return false, err
//	}
//	if acct == nil {
//		return false, ErrAccountNotExist
//	}
//	err = acct.SetCode(code)
//	if err != nil {
//		return false, err
//	}
//	err = am.SetAccount(acct)
//	if err != nil {
//		return false, err
//	}
//	return true, nil
//}

//
//GetCodeSize get code size
func (am *AccountManager) GetCodeSize(accountName common.Name) (uint64, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return 0, err
	}
	if acct == nil {
		return 0, ErrAccountNotExist
	}
	return acct.GetCodeSize(), nil
}

// GetCodeHash get code hash
//func (am *AccountManager) GetCodeHash(accountName common.Name) (common.Hash, error) {
//	acct, err := am.GetAccountByName(accountName)
//	if err != nil {
//		return common.Hash{}, err
//	}
//	if acct == nil {
//		return common.Hash{}, ErrAccountNotExist
//	}
//	return acct.GetCodeHash()
//}

//GetAccountFromValue  get account info via value bytes
func (am *AccountManager) GetAccountFromValue(accountName common.Name, key string, value []byte) (*Account, error) {
	if len(value) == 0 {
		return nil, ErrAccountNotExist
	}
	if key != accountName.String()+acctInfoPrefix {
		return nil, ErrAccountNameInvalid
	}
	var acct Account
	if err := rlp.DecodeBytes(value, &acct); err != nil {
		return nil, ErrAccountNotExist
	}
	if acct.AcctName != accountName {
		return nil, ErrAccountNameInvalid
	}
	return &acct, nil
}

// CanTransfer check if can transfer.
func (am *AccountManager) CanTransfer(accountName common.Name, assetID uint64, value *big.Int) (bool, error) {
	acct, err := am.GetAccountByName(accountName)
	if err != nil {
		return false, err
	}
	if err = acct.EnoughAccountBalance(assetID, value); err == nil {
		return true, nil
	}
	return false, err
}

//TransferAsset transfer asset
func (am *AccountManager) TransferAsset(fromAccount common.Name, toAccount common.Name, assetID uint64, value *big.Int) error {
	if value.Sign() == -1 {
		return ErrNegativeValue
	}

	if ast, err := am.GetAssetInfoByID(assetID); err != nil {
		return err
	} else if len(ast.Contract.String()) != 0 && !(fromAccount == ast.Contract || toAccount == ast.Contract) {
		return ErrInvalidReceiptAsset
	}

	//check from account
	fromAcct, err := am.GetAccountByName(fromAccount)
	if err != nil {
		return err
	}
	if fromAcct == nil {
		return ErrAccountNotExist
	}
	if value.Cmp(big.NewInt(0)) < 0 {
		return ErrAmountValueInvalid
	}
	if fromAccount == toAccount || value.Cmp(big.NewInt(0)) == 0 {
		return nil
	}
	//check from account balance
	val, err := fromAcct.GetBalanceByID(assetID)
	if err != nil {
		return err
	}
	if val.Cmp(big.NewInt(0)) < 0 || val.Cmp(value) < 0 {
		return ErrInsufficientBalance
	}
	fromAcct.SetBalance(assetID, new(big.Int).Sub(val, value))
	//check to account
	toAcct, err := am.GetAccountByName(toAccount)
	if err != nil {
		return err
	}
	if toAcct == nil {
		return ErrAccountNotExist
	}
	if toAcct.IsDestroyed() {
		return ErrAccountIsDestroy
	}
	val, err = toAcct.GetBalanceByID(assetID)
	if err == ErrAccountAssetNotExist {
		toAcct.AddNewAssetByAssetID(assetID, value)
	} else {
		toAcct.SetBalance(assetID, new(big.Int).Add(val, value))
	}
	if err = am.SetAccount(fromAcct); err != nil {
		return err
	}
	return am.SetAccount(toAcct)
}

func (am *AccountManager) IssueAnyAsset(fromName common.Name, asset *asset.AssetObject) error {
	if !am.ast.IsValidOwner(fromName, asset.GetAssetName()) {
		return fmt.Errorf("account %s can not create %s", fromName, asset.GetAssetName())
	}

	if err := am.IssueAsset(asset); err != nil {
		return err
	}
	return nil
}

//IssueAsset issue asset
func (am *AccountManager) IssueAsset(asset *asset.AssetObject) error {
	//check owner
	acct, err := am.GetAccountByName(asset.GetAssetOwner())
	if err != nil {
		return err
	}
	if acct == nil {
		return ErrAccountNotExist
	}
	//check founder
	if len(asset.GetAssetFounder()) > 0 {
		f, err := am.GetAccountByName(asset.GetAssetFounder())
		if err != nil {
			return err
		}
		if f == nil {
			return ErrAccountNotExist
		}
	} else {
		asset.SetAssetFounder(asset.GetAssetOwner())
	}

	if name, err := common.StringToName(asset.GetAssetName()); err == nil {
		accountID, _ := am.GetAccountIDByName(name)
		if accountID > 0 {
			return ErrNameIsExist
		}
	}

	if err := am.ast.IssueAsset(asset.GetAssetName(), asset.GetAssetNumber(), asset.GetSymbol(), asset.GetAssetAmount(), asset.GetDecimals(), asset.GetAssetFounder(), asset.GetAssetOwner(), asset.GetUpperLimit(), asset.GetContract(), asset.GetAssetDetail()); err != nil {
		return err
	}

	//add the asset to owner
	return am.AddAccountBalanceByName(asset.GetAssetOwner(), asset.GetAssetName(), asset.GetAssetAmount())
}

//IncAsset2Acct increase asset and add amount to accout balance
func (am *AccountManager) IncAsset2Acct(fromName common.Name, toName common.Name, assetID uint64, amount *big.Int) error {
	if err := am.ast.IncreaseAsset(fromName, assetID, amount); err != nil {
		return err
	}
	return am.AddAccountBalanceByID(toName, assetID, amount)
}

//AddBalanceByName add balance to account
//func (am *AccountManager) AddBalanceByName(accountName common.Name, assetID uint64, amount *big.Int) error {
//	acct, err := am.GetAccountByName(accountName)
//	if err != nil {
//		return err
//	}
//	if acct == nil {
//		return ErrAccountNotExist
//	}
//	return acct.AddBalanceByID(assetID, amount)
//	rerturn
//}

//Process account action
func (am *AccountManager) Process(accountManagerContext *types.AccountManagerContext) ([]*types.InternalAction, error) {
	snap := am.sdb.Snapshot()
	internalActions, err := am.process(accountManagerContext)
	if err != nil {
		am.sdb.RevertToSnapshot(snap)
	}
	return internalActions, err
}

func (am *AccountManager) process(accountManagerContext *types.AccountManagerContext) ([]*types.InternalAction, error) {
	action := accountManagerContext.Action
	number := accountManagerContext.Number

	var internalActions []*types.InternalAction

	//if !action.CheckValue(accountManagerContext.ChainConfig) {
	//	return nil, ErrAmountValueInvalid
	//}

	//if action.Type() != types.Transfer && action.Recipient() != common.Name(sysName) {
	//	return nil, ErrInvalidReceipt
	//}

	//transfer
	if action.Value().Cmp(big.NewInt(0)) > 0 {
		if err := am.TransferAsset(action.Sender(), action.Recipient(), action.AssetID(), action.Value()); err != nil {
			return nil, err
		}
	}

	//transaction
	switch action.Type() {
	case types.CreateAccount:
		var acct AccountAction
		err := rlp.DecodeBytes(action.Data(), &acct)
		if err != nil {
			return nil, err
		}

		if err := am.CreateAnyAccount(action.Sender(), acct.AccountName, acct.Founder, number, acct.PublicKey, acct.Detail); err != nil {
			return nil, err
		}

		if action.Value().Cmp(big.NewInt(0)) > 0 {
			if err := am.TransferAsset(common.Name(accountManagerContext.ChainConfig.AccountName), acct.AccountName, action.AssetID(), action.Value()); err != nil {
				return nil, err
			}
			actionX := types.NewAction(types.Transfer, common.Name(accountManagerContext.ChainConfig.AccountName), acct.AccountName, 0, action.AssetID(), 0, action.Value(), nil, nil)
			internalAction := &types.InternalAction{Action: actionX.NewRPCAction(0), ActionType: "", GasUsed: 0, GasLimit: 0, Depth: 0, Error: ""}
			internalActions = append(internalActions, internalAction)
		}
		break
	case types.UpdateAccount:
		var acct AccountAction
		err := rlp.DecodeBytes(action.Data(), &acct)
		if err != nil {
			return nil, err
		}

		if err := am.UpdateAccount(action.Sender(), &acct); err != nil {
			return nil, err
		}
		break
	case types.UpdateAccountAuthor:
		var acctAuth AccountAuthorAction
		err := rlp.DecodeBytes(action.Data(), &acctAuth)
		if err != nil {
			return nil, err
		}
		if err := am.UpdateAccountAuthor(action.Sender(), &acctAuth); err != nil {
			return nil, err
		}
		break
	case types.IssueAsset:
		var asset asset.AssetObject
		err := rlp.DecodeBytes(action.Data(), &asset)
		if err != nil {
			return nil, err
		}

		asset.SetAssetNumber(number)
		if err := am.IssueAnyAsset(action.Sender(), &asset); err != nil {
			return nil, err
		}
		actionX := types.NewAction(types.Transfer, common.Name(accountManagerContext.ChainConfig.ChainName), asset.GetAssetOwner(), 0, asset.GetAssetId(), 0, asset.GetAssetAmount(), nil, nil)
		internalAction := &types.InternalAction{Action: actionX.NewRPCAction(0), ActionType: "", GasUsed: 0, GasLimit: 0, Depth: 0, Error: ""}
		internalActions = append(internalActions, internalAction)
		break
	case types.IncreaseAsset:
		var inc IncAsset
		err := rlp.DecodeBytes(action.Data(), &inc)
		if err != nil {
			return nil, err
		}
		if err = am.IncAsset2Acct(action.Sender(), inc.To, inc.AssetId, inc.Amount); err != nil {
			return nil, err
		}
		actionX := types.NewAction(types.Transfer, common.Name(accountManagerContext.ChainConfig.ChainName), inc.To, 0, inc.AssetId, 0, inc.Amount, nil, nil)
		internalAction := &types.InternalAction{Action: actionX.NewRPCAction(0), ActionType: "", GasUsed: 0, GasLimit: 0, Depth: 0, Error: ""}
		internalActions = append(internalActions, internalAction)
		break

	case types.DestroyAsset:
		// var asset asset.AssetObject
		// err := rlp.DecodeBytes(action.Data(), &asset)
		// if err != nil {
		// 	return err
		// }
		if err := am.SubAccountBalanceByID(common.Name(accountManagerContext.ChainConfig.AssetName), action.AssetID(), action.Value()); err != nil {
			return nil, err
		}

		if err := am.ast.DestroyAsset(common.Name(accountManagerContext.ChainConfig.AssetName), action.AssetID(), action.Value()); err != nil {
			return nil, err
		}
		actionX := types.NewAction(types.Transfer, common.Name(accountManagerContext.ChainConfig.AccountName), common.Name(accountManagerContext.ChainConfig.ChainName), 0, action.AssetID(), 0, action.Value(), nil, nil)
		internalAction := &types.InternalAction{Action: actionX.NewRPCAction(0), ActionType: "", GasUsed: 0, GasLimit: 0, Depth: 0, Error: ""}
		internalActions = append(internalActions, internalAction)
		break
	case types.UpdateAsset:
		var asset asset.AssetObject
		err := rlp.DecodeBytes(action.Data(), &asset)
		if err != nil {
			return nil, err
		}
		acct, err := am.GetAccountByName(asset.GetAssetOwner())
		if err != nil {
			return nil, err
		}
		if acct == nil {
			return nil, ErrAccountNotExist
		}
		if len(asset.GetAssetFounder().String()) > 0 {
			acct, err := am.GetAccountByName(asset.GetAssetFounder())
			if err != nil {
				return nil, err
			}
			if acct == nil {
				return nil, ErrAccountNotExist
			}
		}
		if err := am.ast.UpdateAsset(action.Sender(), asset.GetAssetId(), asset.GetAssetOwner(), asset.GetAssetFounder(), asset.GetAssetContract()); err != nil {
			return nil, err
		}
		break
	case types.SetAssetOwner:
		var asset asset.AssetObject
		err := rlp.DecodeBytes(action.Data(), &asset)
		if err != nil {
			return nil, err
		}
		acct, err := am.GetAccountByName(asset.GetAssetOwner())
		if err != nil {
			return nil, err
		}
		if acct == nil {
			return nil, ErrAccountNotExist
		}
		if err := am.ast.SetAssetNewOwner(action.Sender(), asset.GetAssetId(), asset.GetAssetOwner()); err != nil {
			return nil, err
		}
		break
	// case types.SetAssetFounder:
	// 	var asset asset.AssetObject
	// 	err := rlp.DecodeBytes(action.Data(), &asset)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	if len(asset.GetAssetFounder().String()) > 0 {
	// 		acct, err := am.GetAccountByName(asset.GetAssetFounder())
	// 		if err != nil {
	// 			return err
	// 		}
	// 		if acct == nil {
	// 			return ErrAccountNotExist
	// 		}
	// 	}
	// 	if err = am.ast.SetAssetFounder(action.Sender(), asset.GetAssetId(), asset.GetAssetFounder()); err != nil {
	// 		return err
	// 	}
	// 	break
	case types.Transfer:
		//return am.TransferAsset(action.Sender(), action.Recipient(), action.AssetID(), action.Value())
		break
	default:
		return nil, ErrUnkownTxType
	}

	return internalActions, nil
}
