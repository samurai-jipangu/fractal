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

package dpos

import (
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"reflect"
	"testing"

	"github.com/fractalplatform/fractal/types"
	"github.com/fractalplatform/fractal/utils/fdb"
	ldb "github.com/fractalplatform/fractal/utils/fdb/leveldb"
)

type levelDB struct {
	fdb.Database
}

func (ldb *levelDB) Has(key string) (bool, error) {
	return ldb.Database.Has([]byte(key))
}
func (ldb *levelDB) Get(key string) ([]byte, error) {
	if has, err := ldb.Database.Has([]byte(key)); err != nil {
		return nil, err
	} else if !has {
		return nil, nil
	}
	return ldb.Database.Get([]byte(key))
}
func (ldb *levelDB) Put(key string, value []byte) error {
	return ldb.Database.Put([]byte(key), value)
}
func (ldb *levelDB) Delete(key string) error {
	if has, err := ldb.Database.Has([]byte(key)); err != nil {
		return err
	} else if !has {
		return nil
	}
	return ldb.Database.Delete([]byte(key))
}
func (ldb *levelDB) Delegate(string, *big.Int) error {
	return nil
}
func (ldb *levelDB) Undelegate(string, *big.Int) (*types.Action, error) {
	return nil, nil
}
func (ldb *levelDB) IncAsset2Acct(string, string, *big.Int) (*types.Action, error) {
	return nil, nil
}
func (ldb *levelDB) GetSnapshot(string, uint64) ([]byte, error) {
	return nil, nil
}
func (ldb *levelDB) GetBalanceByTime(name string, timestamp uint64) (*big.Int, error) {
	return new(big.Int).Mul(big.NewInt(1000000000), DefaultConfig.decimals()), nil
}
func newTestLDB() (*levelDB, func()) {
	dirname, err := ioutil.TempDir(os.TempDir(), "dpos_test_")
	if err != nil {
		panic("failed to create test file: " + err.Error())
	}
	db, err := ldb.NewLDBDatabase(dirname, 0, 0)
	if err != nil {
		panic("failed to create test database: " + err.Error())
	}

	return &levelDB{Database: db}, func() {
		db.Close()
		os.RemoveAll(dirname)
	}
}

var (
	candidates = []string{
		"candidate1",
		"candidate2",
		"candidate3",
	}
	voters = []string{
		"voter1",
		"voter2",
		"voter3",
	}
)

func TestLDBCandidate(t *testing.T) {
	// SetCandidate(*CandidateInfo) error
	// DelCandidate(string) error
	// GetCandidate(string) (*CandidateInfo, error)
	// GetCandidates() ([]string, error)
	// CandidatesSize() (uint64, error)
	ldb, function := newTestLDB()
	db, _ := NewLDB(ldb)
	defer function()

	for index, candidate := range candidates {
		candidateInfo := &CandidateInfo{
			Name:          candidate,
			URL:           fmt.Sprintf("www.%v.com", candidate),
			Quantity:      big.NewInt(0),
			TotalQuantity: big.NewInt(0),
		}
		if err := db.SetCandidate(candidateInfo); err != nil {
			panic(fmt.Errorf("SetCandidate --- %v", err))
		}
		if nCandidateInfo, err := db.GetCandidate(candidate); err != nil {
			panic(fmt.Errorf("GetCandidate --- %v", err))
		} else if !reflect.DeepEqual(candidateInfo, nCandidateInfo) {
			panic(fmt.Errorf("GetCandidate mismatch"))
		}
		if nCandidates, err := db.GetCandidates(); err != nil {
			panic(fmt.Errorf("GetCandidates --- %v", err))
		} else if len(nCandidates) != index+1 {
			panic(fmt.Errorf("GetCandidates mismatch"))
		}
		if size, err := db.CandidatesSize(); err != nil {
			panic(fmt.Errorf("CandidatesSize --- %v", err))
		} else if size != uint64(index+1) {
			panic(fmt.Errorf("CandidatesSize mismatch"))
		}
	}

	for _, candidate := range candidates {
		candidateInfo, _ := db.GetCandidate(candidate)
		if err := db.SetCandidate(candidateInfo); err != nil {
			panic(fmt.Errorf("Redo SetCandidate --- %v", err))
		}
		if nCandidateInfo, err := db.GetCandidate(candidate); err != nil {
			panic(fmt.Errorf("Redo GetCandidate --- %v", err))
		} else if !reflect.DeepEqual(candidateInfo, nCandidateInfo) {
			panic(fmt.Errorf("Redo GetCandidate mismatch"))
		}
		if nCandidates, err := db.GetCandidates(); err != nil {
			panic(fmt.Errorf("Redo GetCandidates --- %v", err))
		} else if len(nCandidates) != len(candidates) {
			panic(fmt.Errorf("Redo GetCandidates mismatch"))
		}
		if size, err := db.CandidatesSize(); err != nil {
			panic(fmt.Errorf("Redo CandidatesSize --- %v", err))
		} else if size != uint64(len(candidates)) {
			panic(fmt.Errorf("Redo CandidatesSize mismatch"))
		}
	}

	for index, candidate := range candidates {
		if err := db.DelCandidate(candidate); err != nil {
			panic(fmt.Errorf("DelCandidate --- %v", err))
		}
		if nCandidateInfo, err := db.GetCandidate(candidate); err != nil {
			panic(fmt.Errorf("Del GetCandidate --- %v", err))
		} else if nCandidateInfo != nil {
			panic(fmt.Errorf("Del GetCandidate mismatch"))
		}

		if nCandidates, err := db.GetCandidates(); err != nil {
			panic(fmt.Errorf("Del GetCandidates --- %v", err))
		} else if len(nCandidates) != len(candidates)-index-1 {
			panic(fmt.Errorf("Del GetCandidates mismatch"))
		}
		if size, err := db.CandidatesSize(); err != nil {
			panic(fmt.Errorf("Del CandidatesSize --- %v", err))
		} else if size != uint64(len(candidates)-index-1) {
			panic(fmt.Errorf("Del CandidatesSize mismatch"))
		}
	}
}

func TestLDBAvailableQuantity(t *testing.T) {
	// SetAvailableQuantity(uint64, string, *big.Int) error
	// GetAvailableQuantity(uint64, string) (*big.Int, error)
	ldb, function := newTestLDB()
	db, _ := NewLDB(ldb)
	defer function()

	for index, voter := range voters {
		if err := db.SetAvailableQuantity(uint64(index), voter, big.NewInt(int64(index))); err != nil {
			panic(fmt.Errorf("SetAvailableQuantity --- %v", err))
		}

		if quantity, err := db.GetAvailableQuantity(uint64(index), voter); err != nil {
			panic(fmt.Errorf("GetAvailableQuantity --- %v", err))
		} else if quantity.Cmp(big.NewInt(int64(index))) != 0 {
			panic(fmt.Errorf("GetAvailableQuantity mismatch"))
		}
	}

	for index, voter := range voters {
		if err := db.SetAvailableQuantity(uint64(index), voter, big.NewInt(int64(index+1))); err != nil {
			panic(fmt.Errorf("Redo SetAvailableQuantity --- %v", err))
		}

		if quantity, err := db.GetAvailableQuantity(uint64(index), voter); err != nil {
			panic(fmt.Errorf("Redo GetAvailableQuantity --- %v", err))
		} else if quantity.Cmp(big.NewInt(int64(index+1))) != 0 {
			panic(fmt.Errorf("Redo GetAvailableQuantity mismatch"))
		}
	}
}

func TestLDBVoter(t *testing.T) {
	// SetVoter(*VoterInfo) error
	// DelVoter(*VoterInfo) error
	// DelVoters(uint64, string) error
	// GetVoter(uint64, string, string) (*VoterInfo, error)
	// GetVoters(uint64, string) ([]string, error)
	// GetVoterCandidates(uint64, string) ([]string, error)
	ldb, function := newTestLDB()
	db, _ := NewLDB(ldb)
	defer function()

	for _, candidate := range candidates {
		candidateInfo := &CandidateInfo{
			Name:          candidate,
			URL:           fmt.Sprintf("www.%v.com", candidate),
			Quantity:      big.NewInt(0),
			TotalQuantity: big.NewInt(0),
		}
		if err := db.SetCandidate(candidateInfo); err != nil {
			panic(fmt.Errorf("SetCandidate --- %v", err))
		}
	}

	epcho := uint64(0)
	for index, voter := range voters {
		voterInfo := &VoterInfo{
			Epcho:     epcho,
			Name:      voter,
			Candidate: candidates[0],
			Quantity:  big.NewInt(int64(index)),
			Height:    uint64(index),
		}
		if err := db.SetAvailableQuantity(epcho, voter, big.NewInt(int64(index))); err != nil {
			panic(fmt.Errorf("SetAvailableQuantity --- %v", err))
		}
		if err := db.SetVoter(voterInfo); err != nil {
			panic(fmt.Errorf("SetVoter --- %v", err))
		}
		if nvoterInfo, err := db.GetVoter(epcho, voter, candidates[0]); err != nil {
			panic(fmt.Errorf("GetVoter --- %v", err))
		} else if !reflect.DeepEqual(voterInfo, nvoterInfo) {
			panic(fmt.Errorf("GetVoter mismatch"))
		}
		if nCandidates, err := db.GetVotersByVoter(epcho, voter); err != nil {
			panic(fmt.Errorf("GetVotersByVoter --- %v", err))
		} else if len(nCandidates) != 1 {
			panic(fmt.Errorf("GetVotersByVoter mismatch %v %v", len(nCandidates), index+1))
		}
		if nVoters, err := db.GetVotersByCandidate(epcho, candidates[0]); err != nil {
			panic(fmt.Errorf("GetVotersByCandidate --- %v", err))
		} else if len(nVoters) != index+1 {
			panic(fmt.Errorf("GetVotersByCandidate mismatch"))
		}
	}

	epcho++
	for index, candidate := range candidates {
		voterInfo := &VoterInfo{
			Epcho:     epcho,
			Name:      voters[0],
			Candidate: candidate,
			Quantity:  big.NewInt(int64(index)),
			Height:    uint64(index),
		}
		if err := db.SetAvailableQuantity(epcho, voters[0], big.NewInt(int64(index))); err != nil {
			panic(fmt.Errorf("SetAvailableQuantity --- %v", err))
		}
		if err := db.SetVoter(voterInfo); err != nil {
			panic(fmt.Errorf("SetVoter --- %v", err))
		}
		if nvoterInfo, err := db.GetVoter(epcho, voters[0], candidate); err != nil {
			panic(fmt.Errorf("GetVoter --- %v", err))
		} else if !reflect.DeepEqual(voterInfo, nvoterInfo) {
			panic(fmt.Errorf("GetVoter mismatch"))
		}
		if nCandidates, err := db.GetVotersByVoter(epcho, voters[0]); err != nil {
			panic(fmt.Errorf("GetVotersByVoter --- %v", err))
		} else if len(nCandidates) != index+1 {
			panic(fmt.Errorf("GetVotersByVoter mismatch %v %v", len(nCandidates), index+1))
		}
		if nVoters, err := db.GetVotersByCandidate(epcho, candidate); err != nil {
			panic(fmt.Errorf("GetVotersByCandidate --- %v", err))
		} else if len(nVoters) != 1 {
			panic(fmt.Errorf("GetVotersByCandidate mismatch"))
		}
	}

	epcho++
	for index, candidate := range candidates {
		voterInfo := &VoterInfo{
			Epcho:     epcho,
			Name:      voters[index],
			Candidate: candidate,
			Quantity:  big.NewInt(int64(index)),
			Height:    uint64(index),
		}
		if err := db.SetAvailableQuantity(epcho, voters[index], big.NewInt(int64(index))); err != nil {
			panic(fmt.Errorf("SetAvailableQuantity --- %v", err))
		}
		if err := db.SetVoter(voterInfo); err != nil {
			panic(fmt.Errorf("SetVoter --- %v", err))
		}
		if nvoterInfo, err := db.GetVoter(epcho, voters[index], candidate); err != nil {
			panic(fmt.Errorf("GetVoter --- %v", err))
		} else if !reflect.DeepEqual(voterInfo, nvoterInfo) {
			panic(fmt.Errorf("GetVoter mismatch"))
		}
		if nCandidates, err := db.GetVotersByVoter(epcho, voters[index]); err != nil {
			panic(fmt.Errorf("GetVotersByVoter --- %v", err))
		} else if len(nCandidates) != 1 {
			panic(fmt.Errorf("GetVotersByVoter mismatch"))
		}
		if nVoters, err := db.GetVotersByCandidate(epcho, candidate); err != nil {
			panic(fmt.Errorf("GetVotersByCandidate --- %v", err))
		} else if len(nVoters) != 1 {
			panic(fmt.Errorf("GetVotersByCandidate mismatch"))
		}
	}

}

func TestLDBGlobalState(t *testing.T) {
	// SetState(*GlobalState) error
	// GetState(uint64) (*GlobalState, error)
	ldb, function := newTestLDB()
	db, _ := NewLDB(ldb)
	defer function()

	for index := range candidates {
		gstate := &GlobalState{
			Epcho:                      uint64(index + 1),
			PreEpcho:                   uint64(index),
			ActivatedTotalQuantity:     big.NewInt(0),
			ActivatedCandidateSchedule: candidates[index:],
			TotalQuantity:              big.NewInt(0),
		}
		if err := db.SetState(gstate); err != nil {
			panic(fmt.Errorf("SetState --- %v", err))
		}
		if ngstate, err := db.GetState(uint64(index + 1)); err != nil {
			panic(fmt.Errorf("GetState --- %v", err))
		} else if !reflect.DeepEqual(gstate, ngstate) {
			panic(fmt.Errorf("GetState mismatch"))
		}
		if epcho, err := db.GetLastestEpcho(); err != nil {
			panic(fmt.Errorf("GetLastestEpcho --- %v", err))
		} else if epcho != uint64(index+1) {
			panic(fmt.Errorf("GetLastestEpcho mismatch"))
		}
	}

	for index := range candidates {
		gstate := &GlobalState{
			Epcho:                      uint64(index + 1),
			PreEpcho:                   uint64(index),
			ActivatedTotalQuantity:     big.NewInt(0),
			ActivatedCandidateSchedule: candidates[index:],
			TotalQuantity:              big.NewInt(0),
		}
		if err := db.SetState(gstate); err != nil {
			panic(fmt.Errorf("Redo SetState --- %v", err))
		}
		if ngstate, err := db.GetState(uint64(index + 1)); err != nil {
			panic(fmt.Errorf("Redo GetState --- %v", err))
		} else if !reflect.DeepEqual(gstate, ngstate) {
			panic(fmt.Errorf("Redo GetState mismatch"))
		}
		if epcho, err := db.GetLastestEpcho(); err != nil {
			panic(fmt.Errorf("GetLastestEpcho --- %v", err))
		} else if epcho != uint64(index+1) {
			panic(fmt.Errorf("GetLastestEpcho mismatch"))
		}
	}
}
