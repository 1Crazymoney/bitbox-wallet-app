// Copyright 2018 Shift Devices AG
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package coin

import (
	"github.com/digitalbitbox/bitbox-wallet-app/util/observable"
)

// Coin models the currency of a blockchain.
type Coin interface {
	observable.Interface

	// Code returns the code used to identify the coin (should be the acronym of the coin in
	// lowercase).
	Code() string

	// // Type returns the coin type according to BIP44:
	// // https://github.com/satoshilabs/slips/blob/master/slip-0044.md
	// Type() uint32

	// Unit is the unit code for formatting amounts, e.g. "BTC".
	// The fee unit is usually the same as the main unit, but can differ.
	Unit(isFee bool) string

	// Number of decimal places in the standard unit, e.g. 8 for Bitcoin. Must be in the range
	// [0..31].
	Decimals(isFee bool) uint

	// FormatAmount formats the given amount as a number.
	FormatAmount(amount Amount, isFee bool) string

	// ToUnit returns the given amount in the unit as returned above.
	ToUnit(amount Amount, isFee bool) float64

	// // Server returns the host and port of the full node used for blockchain synchronization.
	// Server() string

	// // Returns whether the coin is account-based (instead of UTXO).
	// // Account-based transactions can have only one output and need no change address.
	// AccountBased() bool

	// BlockExplorerTransactionURLPrefix returns the URL prefix of the block explorer.
	BlockExplorerTransactionURLPrefix() string

	// Initialize initializes the coin by connecting to a full node, downloading the headers, etc.
	Initialize()

	// SmallestUnit returns the name of the smallest unit of a given coin
	SmallestUnit() string

	// Close shuts down all resources obtained by the coin (network connections, databases, etc.).
	Close() error
}
