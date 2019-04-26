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
	"math/big"
	"sync/atomic"
	"time"
)

// DefaultConfig configures
var DefaultConfig = &Config{
	MaxURLLen:             512,
	UnitStake:             big.NewInt(1000),
	CandidateMinQuantity:  big.NewInt(10),
	VoterMinQuantity:      big.NewInt(1),
	ActivatedMinQuantity:  big.NewInt(100),
	BlockInterval:         3000,
	BlockFrequency:        6,
	CandidateScheduleSize: 3,
	BackupScheduleSize:    0,
	EpchoInterval:         54000,
	FreezeEpchoSize:       3,
	AccountName:           "ftsystemdpos",
	SystemName:            "ftsystemio",
	SystemURL:             "www.fractalproject.com",
	ExtraBlockReward:      big.NewInt(1),
	BlockReward:           big.NewInt(5),
	Decimals:              18,
	AssetID:               1,
	ReferenceTime:         1555776000000 * uint64(time.Millisecond), // 2019-04-21 00:00:00
}

// Config dpos configures
type Config struct {
	// consensus fileds
	MaxURLLen             uint64   `json:"maxURLLen"`            // url length
	UnitStake             *big.Int `json:"unitStake"`            // state unit
	CandidateMinQuantity  *big.Int `json:"candidateMinQuantity"` // min quantity
	VoterMinQuantity      *big.Int `json:"voterMinQuantity"`     // min quantity
	ActivatedMinQuantity  *big.Int `json:"activatedMinQuantity"` // min active quantity
	BlockInterval         uint64   `json:"blockInterval"`
	BlockFrequency        uint64   `json:"blockFrequency"`
	CandidateScheduleSize uint64   `json:"candidateScheduleSize"`
	BackupScheduleSize    uint64   `json:"backupScheduleSize"`
	EpchoInterval         uint64   `json:"epchoInterval"`
	FreezeEpchoSize       uint64   `json:"freezeEpchoSize"`
	AccountName           string   `json:"accountName"`
	SystemName            string   `json:"systemName"`
	SystemURL             string   `json:"systemURL"`
	ExtraBlockReward      *big.Int `json:"extraBlockReward"`
	BlockReward           *big.Int `json:"blockReward"`
	InitCandidateSchedule []string `json:"initCandidateSchedule"`
	Decimals              uint64   `json:"decimals"`
	AssetID               uint64   `json:"assetID"`
	ReferenceTime         uint64   `json:"referenceTime"`

	// cache files
	decimal     atomic.Value
	blockInter  atomic.Value
	mepochInter atomic.Value
	epochInter  atomic.Value
	safeSize    atomic.Value
}

func (cfg *Config) decimals() *big.Int {
	if decimal := cfg.decimal.Load(); decimal != nil {
		return decimal.(*big.Int)
	}
	decimal := big.NewInt(1)
	for i := uint64(0); i < cfg.Decimals; i++ {
		decimal = new(big.Int).Mul(decimal, big.NewInt(10))
	}
	cfg.decimal.Store(decimal)
	return decimal
}

func (cfg *Config) unitStake() *big.Int {
	return new(big.Int).Mul(cfg.UnitStake, cfg.decimals())
}

func (cfg *Config) extraBlockReward() *big.Int {
	return new(big.Int).Mul(cfg.ExtraBlockReward, cfg.decimals())
}

func (cfg *Config) blockReward() *big.Int {
	return new(big.Int).Mul(cfg.BlockReward, cfg.decimals())
}

func (cfg *Config) blockInterval() uint64 {
	if blockInter := cfg.blockInter.Load(); blockInter != nil {
		return blockInter.(uint64)
	}
	blockInter := cfg.BlockInterval * uint64(time.Millisecond)
	cfg.blockInter.Store(blockInter)
	return blockInter
}
func (cfg *Config) mepochInterval() uint64 {
	if mepochInter := cfg.mepochInter.Load(); mepochInter != nil {
		return mepochInter.(uint64)
	}
	mepochInter := cfg.blockInterval() * cfg.BlockFrequency * cfg.CandidateScheduleSize
	cfg.mepochInter.Store(mepochInter)
	return mepochInter
}
func (cfg *Config) epochInterval() uint64 {
	if epochInter := cfg.epochInter.Load(); epochInter != nil {
		return epochInter.(uint64)
	}
	epochInter := cfg.EpchoInterval * uint64(time.Millisecond)
	cfg.epochInter.Store(epochInter)
	return epochInter
}

func (cfg *Config) consensusSize() uint64 {
	if safeSize := cfg.safeSize.Load(); safeSize != nil {
		return safeSize.(uint64)
	}

	safeSize := cfg.CandidateScheduleSize*2/3 + 1
	cfg.safeSize.Store(safeSize)
	return safeSize
}

func (cfg *Config) slot(timestamp uint64) uint64 {
	return ((timestamp + cfg.blockInterval()/10) / cfg.blockInterval() * cfg.blockInterval())
}

func (cfg *Config) nextslot(timestamp uint64) uint64 {
	return cfg.slot(timestamp) + cfg.blockInterval()
}

func (cfg *Config) getoffset(timestamp uint64) uint64 {
	offset := uint64(timestamp) % cfg.mepochInterval()
	offset /= cfg.blockInterval() * cfg.BlockFrequency
	return offset
}

func (cfg *Config) epoch(timestamp uint64) uint64 {
	return (timestamp-cfg.ReferenceTime)/cfg.epochInterval() + 1
}
