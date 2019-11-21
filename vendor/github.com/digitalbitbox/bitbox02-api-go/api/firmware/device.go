// Copyright 2018-2019 Shift Cryptosecurity AG
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

// Package firmware contains the API to the physical device.
package firmware

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"sync"
	"time"

	"github.com/digitalbitbox/bitbox02-api-go/api/common"
	"github.com/digitalbitbox/bitbox02-api-go/api/firmware/messages"
	"github.com/digitalbitbox/bitbox02-api-go/util/errp"
	"github.com/digitalbitbox/bitbox02-api-go/util/semver"
	"github.com/flynn/noise"
	"github.com/golang/protobuf/proto"
)

//go:generate sh -c "protoc --proto_path=messages/ --go_out='import_path=messages,paths=source_relative:messages' messages/*.proto"

var (
	lowestSupportedFirmwareVersion                   = semver.NewSemVer(4, 2, 1)
	lowestSupportedFirmwareVersionBTCOnly            = semver.NewSemVer(4, 2, 2)
	lowestSupportedFirmwareVersionBitBoxBaseStandard = semver.NewSemVer(4, 3, 0)
	lowestNonSupportedFirmwareVersion                = semver.NewSemVer(5, 0, 0)
)

// Communication contains functions needed to communicate with the device.
type Communication interface {
	SendFrame(string) error
	Query([]byte) ([]byte, error)
	Close()
}

// ConfigInterface lets the library client provide their own persisted config backend.
type ConfigInterface interface {
	// ContainsDeviceStaticPubkey returns true if a device pubkey has been added before.
	ContainsDeviceStaticPubkey(pubkey []byte) bool
	// AddDeviceStaticPubkey adds a device pubkey.
	AddDeviceStaticPubkey(pubkey []byte) error
	// GetAppNoiseStaticKeypair retrieves the app keypair. Returns nil if none has been set before.
	GetAppNoiseStaticKeypair() *noise.DHKey
	// SetAppNoiseStaticKeypair stores the app keypair. Overwrites keypair if one already exists.
	SetAppNoiseStaticKeypair(key *noise.DHKey) error
}

// Logger lets the library client provide their own logging infrastructure.
type Logger interface {
	// err can be nil
	Error(msg string, err error)
	Info(msg string)
	Debug(msg string)
}

// Event instances are sent to the onEvent callback.
type Event string

const (
	// EventChannelHashChanged is fired when the return values of ChannelHash() change.
	EventChannelHashChanged Event = "channelHashChanged"

	// EventStatusChanged is fired when the status changes. Check the status using Status().
	EventStatusChanged Event = "statusChanged"

	// EventAttestationCheckFailed is fired when the device does not pass the attestation signature
	// check, indicating that it might not be an authentic device.
	EventAttestationCheckFailed Event = "attestationCheckFailed"
)

const (
	opICanHasHandShaek          = "h"
	opICanHasPairinVerificashun = "v"
	opNoiseMsg                  = "n"
	opAttestation               = "a"
	opUnlock                    = "u"
	opInfo                      = "i"

	responseSuccess = "\x00"
)

// Device provides the API to communicate with the BitBox02.
type Device struct {
	communication Communication
	// firmware version.
	version *semver.SemVer
	product *common.Product

	config ConfigInterface

	attestation bool

	deviceNoiseStaticPubkey   []byte
	channelHash               string
	channelHashAppVerified    bool
	channelHashDeviceVerified bool
	sendCipher, receiveCipher *noise.CipherState

	status Status

	mu      sync.RWMutex
	onEvent func(Event, interface{})
	log     Logger
}

// DeviceInfo is the data returned from the device info api call.
type DeviceInfo struct {
	Name                      string `json:"name"`
	Version                   string `json:"version"`
	Initialized               bool   `json:"initialized"`
	MnemonicPassphraseEnabled bool   `json:"mnemonicPassphraseEnabled"`
}

// NewDevice creates a new instance of Device.
// version:
//   Can be given if known at the time of instantiation, e.g. by parsing the USB HID product string.
//   It must be provided if the version could be less than 4.3.0.
//   If nil, the version will be queried from the device using the OP_INFO api endpoint. Do this
//   when you are sure the firmware version is bigger or equal to 4.3.0.
// product: same deal as with the version, after 4.3.0 it can be inferred by OP_INFO.
func NewDevice(
	version *semver.SemVer,
	product *common.Product,
	config ConfigInterface,
	communication Communication,
	log Logger,
) *Device {
	if (version == nil) != (product == nil) {
		panic("both version and product have to be specified, or none")
	}
	return &Device{
		communication: communication,
		version:       version,
		product:       product,
		config:        config,
		status:        StatusConnected,
		log:           log,
	}
}

// info uses the opInfo api endpoint to learn about the version, platform/edition, and unlock
// status (true if unlocked).
func (device *Device) info() (*semver.SemVer, common.Product, bool, error) {
	response, err := device.communication.Query([]byte(opInfo))
	if err != nil {
		return nil, "", false, err
	}
	if len(response) < 4 {
		return nil, "", false, errp.New("unexpected response")
	}
	versionStrLen, response := int(response[0]), response[1:]
	versionBytes, response := response[:versionStrLen], response[versionStrLen:]
	version, err := semver.NewSemVerFromString(string(versionBytes))
	if err != nil {
		return nil, "", false, err
	}
	platformByte, response := response[0], response[1:]
	editionByte, response := response[0], response[1:]
	const (
		platformBitBox02   = 0x00
		platformBitBoxBase = 0x01
	)
	products := map[byte]map[byte]common.Product{
		platformBitBox02: {
			0x00: common.ProductBitBox02Multi,
			0x01: common.ProductBitBox02BTCOnly,
		},
		platformBitBoxBase: {
			0x00: common.ProductBitBoxBaseStandard,
		},
	}
	editions, ok := products[platformByte]
	if !ok {
		return nil, "", false, errp.Newf("unrecognized platform: %v", platformByte)
	}
	product, ok := editions[editionByte]
	if !ok {
		return nil, "", false, errp.Newf("unrecognized platform/edition: %v/%v", platformByte, editionByte)
	}

	var unlocked bool
	unlockedByte := response[0]
	switch unlockedByte {
	case 0x00:
		unlocked = false
	case 0x01:
		unlocked = true
	default:
		return nil, "", false, errp.New("unexpected reply")
	}
	return version, product, unlocked, nil
}

// Version returns the firmware version.
func (device *Device) Version() *semver.SemVer {
	if device.version == nil {
		panic("version not set; Init() must be called first")
	}
	return device.version
}

// inferVersionAndProduct either sets the version and product by using OP_INFO if they were not
// provided. In this case, the firmware is assumed to be >=v4.3.0, before that OP_INFO was not
// available.
func (device *Device) inferVersionAndProduct() error {
	// The version has not been provided, so we try to get it from OP_INFO.
	if device.version == nil {
		version, product, _, err := device.info()
		if err != nil {
			return errp.New(
				"OP_INFO unavailable; need to provide version and product via the USB HID descriptor")
		}
		device.log.Info(fmt.Sprintf("OP_INFO: version=%s, product=%s", version, product))

		// sanity check
		if !version.AtLeast(semver.NewSemVer(4, 3, 0)) {
			return errp.New("OP_INFO is not supposed to exist below v4.3.0")
		}

		device.version = version
		device.product = &product
	}
	return nil
}

// Init initializes the device. It changes the status to StatusRequireAppUpgrade if needed,
// otherwise performs the attestation check, unlock, and noise pairing. This call is blocking.
// After this call finishes, Status() will be either:
// - StatusRequireAppUpgrade
// - StatusPairingFailed (pairing rejected on the device)
// - StatusUnpaired (in which the host needs to confirm the pairing with ChannelHashVerify(true))
func (device *Device) Init() error {
	device.attestation = false
	device.deviceNoiseStaticPubkey = nil
	device.channelHash = ""
	device.channelHashAppVerified = false
	device.channelHashDeviceVerified = false
	device.sendCipher = nil
	device.receiveCipher = nil
	device.changeStatus(StatusConnected)

	if err := device.inferVersionAndProduct(); err != nil {
		return err
	}
	if device.version.AtLeast(lowestNonSupportedFirmwareVersion) {
		device.changeStatus(StatusRequireAppUpgrade)
		return nil
	}

	attestation, err := device.performAttestation()
	if err != nil {
		return err
	}
	device.attestation = attestation
	device.log.Info(fmt.Sprintf("attestation check result: %v", attestation))

	// Before 2.0.0, unlock was invoked automatically by the device before USB communication
	// started.
	if device.version.AtLeast(semver.NewSemVer(2, 0, 0)) {
		_, err := device.communication.Query([]byte(opUnlock))
		if err != nil {
			// Most likely the device has been unplugged.
			return err
		}
	}

	if err := device.pair(); err != nil {
		// Most likely the device has been unplugged.
		return err
	}

	return nil
}

func (device *Device) pair() error {
	cipherSuite := noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256)
	keypair := device.config.GetAppNoiseStaticKeypair()
	if keypair == nil {
		device.log.Info("noise static keypair created")
		kp, err := cipherSuite.GenerateKeypair(rand.Reader)
		if err != nil {
			panic(err)
		}
		keypair = &kp
		if err := device.config.SetAppNoiseStaticKeypair(keypair); err != nil {
			device.log.Error("could not store app noise static keypair", err)

			// Not a critical error, ignore.
		}
	}
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:   cipherSuite,
		Random:        rand.Reader,
		Pattern:       noise.HandshakeXX,
		StaticKeypair: *keypair,
		Prologue:      []byte("Noise_XX_25519_ChaChaPoly_SHA256"),
		Initiator:     true,
	})
	if err != nil {
		panic(err)
	}
	responseBytes, err := device.communication.Query([]byte(opICanHasHandShaek))
	if err != nil {
		return err
	}
	if string(responseBytes) != responseSuccess {
		panic(string(responseBytes))
	}
	// do handshake:
	msg, _, _, err := handshake.WriteMessage(nil, nil)
	if err != nil {
		panic(err)
	}
	responseBytes, err = device.communication.Query(msg)
	if err != nil {
		return err
	}
	_, _, _, err = handshake.ReadMessage(nil, responseBytes)
	if err != nil {
		panic(err)
	}
	msg, device.sendCipher, device.receiveCipher, err = handshake.WriteMessage(nil, nil)
	if err != nil {
		panic(err)
	}
	responseBytes, err = device.communication.Query(msg)
	if err != nil {
		return err
	}

	device.deviceNoiseStaticPubkey = handshake.PeerStatic()
	if len(device.deviceNoiseStaticPubkey) != 32 {
		panic(errp.New("expected 32 byte remote static pubkey"))
	}

	pairingVerificationRequiredByApp := !device.config.ContainsDeviceStaticPubkey(
		device.deviceNoiseStaticPubkey)
	pairingVerificationRequiredByDevice := string(responseBytes) == "\x01"

	if pairingVerificationRequiredByDevice || pairingVerificationRequiredByApp {
		device.log.Info(fmt.Sprintf(
			"pairing required, byDevice=%v, byApp=%v",
			pairingVerificationRequiredByDevice, pairingVerificationRequiredByApp))
		channelHashBase32 := base32.StdEncoding.EncodeToString(handshake.ChannelBinding())
		device.channelHash = fmt.Sprintf(
			"%s %s\n%s %s",
			channelHashBase32[:5],
			channelHashBase32[5:10],
			channelHashBase32[10:15],
			channelHashBase32[15:20])
		device.fireEvent(EventChannelHashChanged)
		device.changeStatus(StatusUnpaired)

		response, err := device.communication.Query([]byte(opICanHasPairinVerificashun))
		if err != nil {
			return err
		}
		device.channelHashDeviceVerified = string(response) == responseSuccess
		if device.channelHashDeviceVerified {
			device.fireEvent(EventChannelHashChanged)
		} else {
			device.sendCipher = nil
			device.receiveCipher = nil
			device.channelHash = ""
			device.changeStatus(StatusPairingFailed)
		}

	} else {
		device.channelHashDeviceVerified = true
		device.ChannelHashVerify(true)
	}
	return nil
}

func (device *Device) changeStatus(status Status) {
	device.status = status
	device.fireEvent(EventStatusChanged)
}

// Status returns the device state. See the Status* constants.
func (device *Device) Status() Status {
	device.log.Debug(fmt.Sprintf("Device status: %v", device.status))
	return device.status
}

// SetOnEvent installs the callback which will be called with various events.
func (device *Device) SetOnEvent(onEvent func(Event, interface{})) {
	device.mu.Lock()
	defer device.mu.Unlock()
	device.onEvent = onEvent
}

// fireEvent calls device.onEvent callback if non-nil.
// It blocks for the entire duration of the call.
// The read-only lock is released before calling device.onEvent.
func (device *Device) fireEvent(event Event) {
	device.mu.RLock()
	f := device.onEvent
	device.mu.RUnlock()
	if f != nil {
		device.log.Info(fmt.Sprintf("fire event: %s", event))
		f(event, nil)
	}
}

// Close implements device.Device.
func (device *Device) Close() {
	device.communication.Close()
}

func (device *Device) requestBytesEncrypted(request proto.Message) ([]byte, error) {
	if device.sendCipher == nil || !device.channelHashDeviceVerified || !device.channelHashAppVerified {
		return nil, errp.New("handshake must come first")
	}
	requestBytes, err := proto.Marshal(request)
	if err != nil {
		return nil, errp.WithStack(err)
	}
	requestBytesEncrypted := device.sendCipher.Encrypt(nil, nil, requestBytes)
	if device.version.AtLeast(semver.NewSemVer(4, 0, 0)) {
		requestBytesEncrypted = append([]byte(opNoiseMsg), requestBytesEncrypted...)
	}
	return requestBytesEncrypted, nil
}

func (device *Device) query(request proto.Message) (*messages.Response, error) {
	requestBytesEncrypted, err := device.requestBytesEncrypted(request)
	if err != nil {
		return nil, err
	}
	responseBytes, err := device.communication.Query(requestBytesEncrypted)
	if err != nil {
		return nil, err
	}
	responseBytesDecrypted, err := device.receiveCipher.Decrypt(nil, nil, responseBytes)
	if err != nil {
		return nil, errp.WithStack(err)
	}
	response := &messages.Response{}
	if err := proto.Unmarshal(responseBytesDecrypted, response); err != nil {
		return nil, errp.WithStack(err)
	}

	if errorResponse, ok := response.Response.(*messages.Response_Error); ok {
		return nil, errp.WithStack(NewError(errorResponse.Error.Code, errorResponse.Error.Message))
	}

	return response, nil
}

// Random requests a random number from the device using protobuf messages
func (device *Device) Random() ([]byte, error) {
	request := &messages.Request{
		Request: &messages.Request_RandomNumber{
			RandomNumber: &messages.RandomNumberRequest{},
		},
	}

	response, err := device.query(request)
	if err != nil {
		return nil, err
	}

	randomResponse, ok := response.Response.(*messages.Response_RandomNumber)
	if !ok {
		return nil, errp.New("expected RandomNumberResponse response")
	}

	return randomResponse.RandomNumber.Number, nil
}

// SetDeviceName sends a request to the device using protobuf to set the device name
func (device *Device) SetDeviceName(deviceName string) error {
	request := &messages.Request{
		Request: &messages.Request_DeviceName{
			DeviceName: &messages.SetDeviceNameRequest{
				Name: deviceName,
			},
		},
	}

	response, err := device.query(request)
	if err != nil {
		return err
	}

	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("Failed to set device name")
	}

	return nil
}

// DeviceInfo retrieves the current device info from the bitbox
func (device *Device) DeviceInfo() (*DeviceInfo, error) {
	request := &messages.Request{
		Request: &messages.Request_DeviceInfo{
			DeviceInfo: &messages.DeviceInfoRequest{},
		},
	}

	response, err := device.query(request)
	if err != nil {
		return nil, err
	}

	deviceInfoResponse, ok := response.Response.(*messages.Response_DeviceInfo)
	if !ok {
		return nil, errp.New("Failed to retrieve device info")
	}

	deviceInfo := &DeviceInfo{
		Name:                      deviceInfoResponse.DeviceInfo.Name,
		Version:                   deviceInfoResponse.DeviceInfo.Version,
		Initialized:               deviceInfoResponse.DeviceInfo.Initialized,
		MnemonicPassphraseEnabled: deviceInfoResponse.DeviceInfo.MnemonicPassphraseEnabled,
	}

	return deviceInfo, nil
}

// SetPassword invokes the set password workflow on the device. Should be called only if
// deviceInfo.Initialized is false.
func (device *Device) SetPassword() error {
	if device.status == StatusInitialized {
		return errp.New("invalid status")
	}
	request := &messages.Request{
		Request: &messages.Request_SetPassword{
			SetPassword: &messages.SetPasswordRequest{
				Entropy: bytesOrPanic(32),
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}

	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	device.changeStatus(StatusSeeded)
	return nil
}

// CreateBackup is called after SetPassword() to create the backup.
func (device *Device) CreateBackup() error {
	if device.status != StatusSeeded && device.status != StatusInitialized {
		return errp.New("invalid status")
	}

	now := time.Now()
	_, offset := now.Zone()

	request := &messages.Request{
		Request: &messages.Request_CreateBackup{
			CreateBackup: &messages.CreateBackupRequest{
				Timestamp:      uint32(now.Unix()),
				TimezoneOffset: int32(offset),
			},
		},
	}

	response, err := device.query(request)
	if err != nil {
		return err
	}

	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	device.changeStatus(StatusInitialized)
	return nil
}

// Backup contains the metadata of one backup.
type Backup struct {
	ID   string
	Name string
	Time time.Time
}

// ListBackups returns a list of all backups on the SD card.
func (device *Device) ListBackups() ([]*Backup, error) {
	request := &messages.Request{
		Request: &messages.Request_ListBackups{
			ListBackups: &messages.ListBackupsRequest{},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return nil, err
	}
	listBackupsResponse, ok := response.Response.(*messages.Response_ListBackups)
	if !ok {
		return nil, errp.New("unexpected response")
	}
	msgBackups := listBackupsResponse.ListBackups.Info
	backups := make([]*Backup, len(msgBackups))
	for index, msgBackup := range msgBackups {
		backups[index] = &Backup{
			ID:   msgBackup.Id,
			Name: msgBackup.Name,
			Time: time.Unix(int64(msgBackup.Timestamp), 0).Local(),
		}
	}
	return backups, nil
}

// CheckBackup checks if any backup on the SD card matches the current seed on the device
// and returns the name and ID of the matching backup
func (device *Device) CheckBackup(silent bool) (string, error) {
	request := &messages.Request{
		Request: &messages.Request_CheckBackup{
			CheckBackup: &messages.CheckBackupRequest{
				Silent: silent,
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return "", err
	}
	backup, ok := response.Response.(*messages.Response_CheckBackup)
	if !ok {
		return "", errp.New("unexpected response")
	}
	return backup.CheckBackup.Id, nil
}

// RestoreBackup restores a backup returned by ListBackups (id).
func (device *Device) RestoreBackup(id string) error {
	now := time.Now()
	_, offset := now.Zone()
	request := &messages.Request{
		Request: &messages.Request_RestoreBackup{
			RestoreBackup: &messages.RestoreBackupRequest{
				Id:             id,
				Timestamp:      uint32(now.Unix()),
				TimezoneOffset: int32(offset),
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}
	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	device.changeStatus(StatusInitialized)
	return nil
}

// ChannelHash returns the hashed handshake channel binding.
func (device *Device) ChannelHash() (string, bool) {
	return device.channelHash, device.channelHashDeviceVerified
}

// ChannelHashVerify verifies the ChannelHash.
func (device *Device) ChannelHashVerify(ok bool) {
	device.log.Info(fmt.Sprintf("channelHashVerify: %v", ok))
	if ok && !device.channelHashDeviceVerified {
		return
	}
	device.channelHashAppVerified = ok
	if ok {
		// No critical error, we will just need to re-confirm the pairing next time.
		_ = device.config.AddDeviceStaticPubkey(device.deviceNoiseStaticPubkey)
		requireUpgrade := false
		switch *device.product {
		case common.ProductBitBox02Multi:
			requireUpgrade = !device.version.AtLeast(lowestSupportedFirmwareVersion)
		case common.ProductBitBox02BTCOnly:
			requireUpgrade = !device.version.AtLeast(lowestSupportedFirmwareVersionBTCOnly)
		case common.ProductBitBoxBaseStandard:
			requireUpgrade = !device.version.AtLeast(lowestSupportedFirmwareVersionBitBoxBaseStandard)
		default:
			device.log.Error(fmt.Sprintf("unrecognized product: %s", *device.product), nil)
		}
		if requireUpgrade {
			device.changeStatus(StatusRequireFirmwareUpgrade)
			return
		}
		if *device.product == common.ProductBitBoxBaseStandard {
			// For now, the base has no keystore or password.
			device.changeStatus(StatusUninitialized)
		} else {
			info, err := device.DeviceInfo()
			if err != nil {
				device.log.Error("could not get device info", err)
				return
			}
			if info.Initialized {
				device.changeStatus(StatusInitialized)
			} else {
				device.changeStatus(StatusUninitialized)
			}
		}
	} else {
		device.changeStatus(StatusPairingFailed)
	}
}

// CheckSDCard checks whether an sd card is inserted in the device
func (device *Device) CheckSDCard() (bool, error) {
	request := &messages.Request{
		Request: &messages.Request_CheckSdcard{
			CheckSdcard: &messages.CheckSDCardRequest{},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return false, err
	}
	sdCardInserted, ok := response.Response.(*messages.Response_CheckSdcard)
	if !ok {
		return false, errp.New("unexpected response")
	}
	return sdCardInserted.CheckSdcard.Inserted, nil
}

// InsertRemoveSDCard sends a command to the device to insert of remove the sd card based on the workflow state
func (device *Device) InsertRemoveSDCard(action messages.InsertRemoveSDCardRequest_SDCardAction) error {
	request := &messages.Request{
		Request: &messages.Request_InsertRemoveSdcard{
			InsertRemoveSdcard: &messages.InsertRemoveSDCardRequest{
				Action: action,
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}
	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	return nil
}

// SetMnemonicPassphraseEnabled enables or disables entering a mnemonic passphrase after the normal
// unlock.
func (device *Device) SetMnemonicPassphraseEnabled(enabled bool) error {
	request := &messages.Request{
		Request: &messages.Request_SetMnemonicPassphraseEnabled{
			SetMnemonicPassphraseEnabled: &messages.SetMnemonicPassphraseEnabledRequest{
				Enabled: enabled,
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}
	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	return nil
}

func (device *Device) reboot() error {
	request := &messages.Request{
		Request: &messages.Request_Reboot{
			Reboot: &messages.RebootRequest{},
		},
	}
	requestBytesEncrypted, err := device.requestBytesEncrypted(request)
	if err != nil {
		return err
	}
	return device.communication.SendFrame(string(requestBytesEncrypted))
}

// UpgradeFirmware reboots into the bootloader so a firmware can be flashed.
func (device *Device) UpgradeFirmware() error {
	return device.reboot()
}

// Reset factory resets the device. You must call device.Init() afterwards.
func (device *Device) Reset() error {
	request := &messages.Request{
		Request: &messages.Request_Reset_{
			Reset_: &messages.ResetRequest{},
		},
	}
	_, err := device.query(request)
	return err
}

// ShowMnemonic lets the user export the bip39 mnemonic phrase on the device.
func (device *Device) ShowMnemonic() error {
	request := &messages.Request{
		Request: &messages.Request_ShowMnemonic{
			ShowMnemonic: &messages.ShowMnemonicRequest{},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}
	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	return nil
}

// RestoreFromMnemonic invokes the mnemonic phrase import workflow.
func (device *Device) RestoreFromMnemonic() error {
	now := time.Now()
	_, offset := now.Zone()
	request := &messages.Request{
		Request: &messages.Request_RestoreFromMnemonic{
			RestoreFromMnemonic: &messages.RestoreFromMnemonicRequest{
				Timestamp:      uint32(now.Unix()),
				TimezoneOffset: int32(offset),
			},
		},
	}
	response, err := device.query(request)
	if err != nil {
		return err
	}
	_, ok := response.Response.(*messages.Response_Success)
	if !ok {
		return errp.New("unexpected response")
	}
	device.changeStatus(StatusInitialized)
	return nil
}

// Product returns the device product.
func (device *Device) Product() common.Product {
	if device.product == nil {
		panic("product not set; Init() must be called first")
	}
	return *device.product
}

// SupportsETH returns true if ETH is supported by the device api.
// coinCode is eth/teth/reth or eth-erc20-xyz, ...
func (device *Device) SupportsETH(coinCode string) bool {
	if *device.product != common.ProductBitBox02Multi {
		return false
	}
	if device.version.AtLeast(semver.NewSemVer(4, 0, 0)) {
		switch coinCode {
		case "eth", "reth", "teth":
			return true
		case "eth-erc20-usdt", "eth-erc20-link", "eth-erc20-bat", "eth-erc20-mkr", "eth-erc20-zrx", "eth-erc20-dai":
			return true
		}
	}
	return false
}

// SupportsLTC returns true if LTC is supported by the device api.
func (device *Device) SupportsLTC() bool {
	return *device.product == common.ProductBitBox02Multi
}
