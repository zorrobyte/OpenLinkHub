package razer

// Package: Razer shared HID protocol
// This package implements the 90-byte Razer HID feature report protocol
// used by keyboards, mice, mousepads, and accessories.
// License: GPL-3.0 or later

import (
	"encoding/binary"
	"fmt"
	"github.com/sstallion/go-hid"
	"time"
)

const (
	ReportSize         = 91
	ReportID           = 0x00
	StatusNew          = 0x00
	StatusBusy         = 0x01
	StatusOK           = 0x02
	StatusFail         = 0x03
	StatusTimeout      = 0x04
	StatusNotSupported = 0x05
	MatrixTypeStandard = 1 // Command class 0x03
	MatrixTypeExtended = 2 // Command class 0x0F
)

// RazerReport is the 90-byte HID feature report used by Razer devices
type RazerReport struct {
	Status           byte
	TransactionID    byte
	RemainingPackets uint16
	ProtocolType     byte
	DataSize         byte
	CommandClass     byte
	CommandID        byte
	Arguments        [80]byte
}

// LED IDs (from OpenRGB's RazerController.h)
const (
	LEDIDZero      = 0x00 // Mousepads, docks, accessories
	LEDIDBacklight = 0x05 // Keyboards
)

// RazerDevice holds per-device metadata from the device table
type RazerDevice struct {
	PID           uint16
	Name          string
	DeviceType    string // "keyboard", "mousepad", "accessory", "headset"
	TransactionID byte
	MatrixType    int
	LEDID         byte // LED ID for effects: 0x05 (backlight) for keyboards, 0x00 for accessories
	Rows          int
	Cols          int
	LEDCount      int
}

// DeviceTable maps Razer product IDs to device metadata.
// Expand this table as new devices are added.
var DeviceTable = map[uint16]RazerDevice{
	0x0287: {0x0287, "Razer BlackWidow V4", "keyboard", 0x1F, MatrixTypeExtended, LEDIDBacklight, 6, 22, 132},
	0x0C06: {0x0C06, "Razer Goliathus Chroma 3XL", "mousepad", 0x3F, MatrixTypeExtended, LEDIDZero, 1, 1, 1},
	0x00A4: {0x00A4, "Razer Mouse Dock Pro", "accessory", 0xFF, MatrixTypeExtended, LEDIDZero, 1, 8, 8},
	0x0527: {0x0527, "Razer Kraken Ultimate", "headset", 0x00, 0, LEDIDZero, 1, 1, 1},
}

// NewReport creates a new RazerReport with the given parameters
func NewReport(transactionID, class, id, dataSize byte) *RazerReport {
	return &RazerReport{
		Status:        StatusNew,
		TransactionID: transactionID,
		CommandClass:  class,
		CommandID:     id,
		DataSize:      dataSize,
	}
}

// CalculateCRC computes the XOR checksum over bytes 3..88 of the wire format
// (matching OpenRGB: skip report_id, status, transaction_id)
func (r *RazerReport) CalculateCRC() byte {
	buf := r.ToBytes()
	var crc byte
	for i := 3; i < 89; i++ {
		crc ^= buf[i]
	}
	return crc
}

// ToBytes serializes the report to a 91-byte wire format matching OpenRGB's razer_report struct.
// Layout: [report_id(1), status(1), transaction_id(1), remaining_packets(2), protocol_type(1),
//          data_size(1), command_class(1), command_id(1), arguments(80), crc(1), reserved(1)]
func (r *RazerReport) ToBytes() [ReportSize]byte {
	var buf [ReportSize]byte
	buf[0] = ReportID
	buf[1] = r.Status
	buf[2] = r.TransactionID
	binary.BigEndian.PutUint16(buf[3:5], r.RemainingPackets)
	buf[5] = r.ProtocolType
	buf[6] = r.DataSize
	buf[7] = r.CommandClass
	buf[8] = r.CommandID
	copy(buf[9:89], r.Arguments[:])
	// CRC over bytes 3..88 (matching OpenRGB: skip report_id, status, transaction_id)
	var crc byte
	for i := 3; i < 89; i++ {
		crc ^= buf[i]
	}
	buf[89] = crc
	buf[90] = 0x00 // Reserved
	return buf
}

// FromBytes deserializes a 90-byte wire buffer into the report struct
func (r *RazerReport) FromBytes(buf []byte) {
	if len(buf) < ReportSize {
		return
	}
	r.Status = buf[1]
	r.TransactionID = buf[2]
	r.RemainingPackets = binary.BigEndian.Uint16(buf[3:5])
	r.ProtocolType = buf[5]
	r.DataSize = buf[6]
	r.CommandClass = buf[7]
	r.CommandID = buf[8]
	copy(r.Arguments[:], buf[9:89])
}

// SendReport sends a feature report and reads the response.
// Razer devices use HID feature reports (Set_Report / Get_Report).
func SendReport(dev *hid.Device, report *RazerReport) (*RazerReport, error) {
	buf := report.ToBytes()

	// Send the feature report
	_, err := dev.SendFeatureReport(buf[:])
	if err != nil {
		return nil, fmt.Errorf("razer: SendFeatureReport failed: %w", err)
	}

	// Brief delay for the device to process the command (matches OpenRGB timing)
	time.Sleep(5 * time.Millisecond)

	// Read the response
	resp := make([]byte, ReportSize)
	resp[0] = ReportID
	_, err = dev.GetFeatureReport(resp)
	if err != nil {
		return nil, fmt.Errorf("razer: GetFeatureReport failed: %w", err)
	}

	response := &RazerReport{}
	response.FromBytes(resp)

	if response.Status == StatusFail {
		return response, fmt.Errorf("razer: device returned error status for cmd 0x%02X:0x%02X", report.CommandClass, report.CommandID)
	}
	if response.Status == StatusNotSupported {
		return response, fmt.Errorf("razer: command not supported 0x%02X:0x%02X", report.CommandClass, report.CommandID)
	}

	return response, nil
}

// SendReportNoResponse sends a feature report without reading a response.
// Used for write-only commands like SetStaticColor where no response is needed.
func SendReportNoResponse(dev *hid.Device, report *RazerReport) error {
	buf := report.ToBytes()
	_, err := dev.SendFeatureReport(buf[:])
	if err != nil {
		return fmt.Errorf("razer: SendFeatureReport failed: %w", err)
	}
	time.Sleep(5 * time.Millisecond)
	return nil
}

// SetDeviceMode sets the device to software (0x03) or hardware (0x00) mode.
// Software mode is required for host-controlled RGB.
func SetDeviceMode(dev *hid.Device, txID byte, mode byte) error {
	report := NewReport(txID, 0x00, 0x04, 0x02)
	report.Arguments[0] = mode // 0x03 = software, 0x00 = hardware
	report.Arguments[1] = 0x00
	return SendReportNoResponse(dev, report)
}

// GetFirmwareVersion reads the firmware version string from the device
func GetFirmwareVersion(dev *hid.Device, txID byte) (string, error) {
	report := NewReport(txID, 0x00, 0x81, 0x00)
	resp, err := SendReport(dev, report)
	if err != nil {
		return "n/a", err
	}
	major := resp.Arguments[0]
	minor := resp.Arguments[1]
	return fmt.Sprintf("%d.%d", major, minor), nil
}

// GetSerialNumber reads the serial number from the device
func GetSerialNumber(dev *hid.Device, txID byte) (string, error) {
	report := NewReport(txID, 0x00, 0x82, 0x00)
	resp, err := SendReport(dev, report)
	if err != nil {
		return "", err
	}
	// Serial is a null-terminated ASCII string in arguments
	serial := ""
	for i := 0; i < 22; i++ {
		if resp.Arguments[i] == 0 {
			break
		}
		serial += string(resp.Arguments[i])
	}
	return serial, nil
}

// matrixClass returns the command class byte for the given matrix type
func matrixClass(matrixType int) byte {
	if matrixType == MatrixTypeExtended {
		return 0x0F
	}
	return 0x03
}

// SetBrightness sets the global brightness (0-255) on the device
func SetBrightness(dev *hid.Device, txID byte, matrixType int, ledID byte, brightness byte) error {
	class := matrixClass(matrixType)
	report := NewReport(txID, class, 0x04, 0x03)
	report.Arguments[0] = 0x01 // Variable storage
	report.Arguments[1] = ledID
	report.Arguments[2] = brightness
	return SendReportNoResponse(dev, report)
}

// GetBrightness reads the current brightness level from the device
func GetBrightness(dev *hid.Device, txID byte, matrixType int, ledID byte) (byte, error) {
	class := matrixClass(matrixType)
	report := NewReport(txID, class, 0x84, 0x03)
	report.Arguments[0] = 0x01 // Variable storage
	report.Arguments[1] = ledID
	resp, err := SendReport(dev, report)
	if err != nil {
		return 0, err
	}
	return resp.Arguments[2], nil
}

// SetModeNone turns off all LEDs (effect mode "none")
func SetModeNone(dev *hid.Device, txID byte, matrixType int, ledID byte) error {
	class := matrixClass(matrixType)
	report := NewReport(txID, class, 0x02, 0x09)
	report.Arguments[0] = 0x00 // NOSTORE
	report.Arguments[1] = ledID
	report.Arguments[2] = 0x00 // Effect: none
	return SendReportNoResponse(dev, report)
}

// SetStaticColor sets a single static color on the device.
// Uses the extended matrix effect command (class 0x0F, cmd 0x02) with effect ID 0x01 (static).
// ledID should be LEDIDBacklight (0x05) for keyboards, LEDIDZero (0x00) for accessories.
func SetStaticColor(dev *hid.Device, txID byte, matrixType int, ledID byte, r, g, b byte) error {
	class := matrixClass(matrixType)
	report := NewReport(txID, class, 0x02, 0x09)
	report.Arguments[0] = 0x00 // NOSTORE (matches OpenRGB)
	report.Arguments[1] = ledID
	report.Arguments[2] = 0x01 // Effect: static
	// Arguments[3] and [4] are zero padding
	report.Arguments[5] = 0x01 // Color count
	report.Arguments[6] = r
	report.Arguments[7] = g
	report.Arguments[8] = b
	return SendReportNoResponse(dev, report)
}

// SetCustomFrame writes per-LED color data for a row of the LED matrix.
// This is used for custom per-LED coloring via the extended matrix protocol.
func SetCustomFrame(dev *hid.Device, txID byte, matrixType int, row byte, startCol, stopCol byte, colors []byte) error {
	class := matrixClass(matrixType)
	count := int(stopCol-startCol) + 1
	dataSize := byte(5 + count*3) // row(1) + startCol(1) + stopCol(1) + 2 reserved + RGB data
	report := NewReport(txID, class, 0x03, dataSize)
	report.Arguments[0] = row
	report.Arguments[1] = startCol
	report.Arguments[2] = stopCol
	// Arguments[3] and [4] are reserved/padding
	copy(report.Arguments[5:], colors[:count*3])
	return SendReportNoResponse(dev, report)
}

// ApplyCustomFrame tells the device to display the custom frame data previously sent.
// This uses the effect command (0x02) with effect ID 0x08 (custom frame).
func ApplyCustomFrame(dev *hid.Device, txID byte, matrixType int, ledID byte) error {
	class := matrixClass(matrixType)
	report := NewReport(txID, class, 0x02, 0x0C)
	report.Arguments[0] = 0x00 // NOSTORE
	report.Arguments[1] = ledID
	report.Arguments[2] = 0x08 // Effect: custom frame
	return SendReportNoResponse(dev, report)
}

// ============================================================================
// Kraken Protocol
// ============================================================================
// The Razer Kraken headsets use a different 37-byte HID report protocol.
// Reports are sent via hid_write (output reports), not feature reports.
// Format: [report_id(1), destination(1), length(1), addr_h(1), addr_l(1), arguments(32)]

const (
	KrakenReportSize = 37
)

// KrakenDeviceConfig holds per-PID Kraken addresses
type KrakenDeviceConfig struct {
	LedModeAddr    uint16
	CustomAddr     uint16
	BreathingAddr0 uint16
}

// KrakenConfigs maps PIDs to their address configuration
var KrakenConfigs = map[uint16]KrakenDeviceConfig{
	0x0527: {0x172D, 0x1189, 0x1741}, // Kraken Ultimate
	// Add more Kraken PIDs here as needed (V2, Kitty, etc.)
}

// KrakenReport creates a 37-byte Kraken HID output report
func KrakenReport(reportID, destination, length byte, address uint16) [KrakenReportSize]byte {
	var buf [KrakenReportSize]byte
	buf[0] = reportID
	buf[1] = destination
	buf[2] = length
	buf[3] = byte(address >> 8)
	buf[4] = byte(address & 0xFF)
	return buf
}

// KrakenSetStaticColor sets a static color on a Kraken headset.
// The Kraken requires resetting the mode to "none" before applying a new color.
// Sequence: mode none → RGB data → effect static+sync.
func KrakenSetStaticColor(dev *hid.Device, cfg KrakenDeviceConfig, r, g, b byte) error {
	// Step 1: Reset mode to "none" — Kraken ignores new colors without this
	noneReport := KrakenReport(0x04, 0x40, 0x01, cfg.LedModeAddr)
	noneReport[5] = 0x00 // all effect bits off

	_, err := dev.Write(noneReport[:])
	if err != nil {
		return fmt.Errorf("kraken: write mode none: %w", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Step 2: Write RGB color
	rgbReport := KrakenReport(0x04, 0x40, 0x03, cfg.BreathingAddr0)
	rgbReport[5] = r
	rgbReport[6] = g
	rgbReport[7] = b

	_, err = dev.Write(rgbReport[:])
	if err != nil {
		return fmt.Errorf("kraken: write rgb: %w", err)
	}
	time.Sleep(5 * time.Millisecond)

	// Step 3: Write effect mode: on_off_static(bit0) + sync(bit3) = 0x09
	effectReport := KrakenReport(0x04, 0x40, 0x01, cfg.LedModeAddr)
	effectReport[5] = 0x09

	_, err = dev.Write(effectReport[:])
	if err != nil {
		return fmt.Errorf("kraken: write effect: %w", err)
	}
	time.Sleep(5 * time.Millisecond)

	return nil
}

// KrakenSetModeNone turns off LEDs on a Kraken headset.
func KrakenSetModeNone(dev *hid.Device, cfg KrakenDeviceConfig) error {
	report := KrakenReport(0x04, 0x40, 0x01, cfg.LedModeAddr)
	report[5] = 0x00 // all bits off

	_, err := dev.Write(report[:])
	if err != nil {
		return fmt.Errorf("kraken: write off: %w", err)
	}
	time.Sleep(5 * time.Millisecond)
	return nil
}

// KrakenGetFirmware reads the firmware version from a Kraken headset.
func KrakenGetFirmware(dev *hid.Device) (string, error) {
	report := KrakenReport(0x04, 0x20, 0x02, 0x0030)
	_, err := dev.Write(report[:])
	if err != nil {
		return "n/a", fmt.Errorf("kraken: write firmware request: %w", err)
	}
	time.Sleep(5 * time.Millisecond)

	resp := make([]byte, KrakenReportSize)
	n, err := dev.ReadWithTimeout(resp, 500)
	if err != nil || n == 0 {
		return "n/a", fmt.Errorf("kraken: read firmware response: %w", err)
	}

	if resp[0] == 0x05 && n >= 4 {
		return fmt.Sprintf("v%d.%d", resp[2], resp[3]), nil
	}
	return "n/a", nil
}

// KrakenGetSerial reads the serial number from a Kraken headset.
func KrakenGetSerial(dev *hid.Device) (string, error) {
	report := KrakenReport(0x04, 0x20, 0x16, 0x7F00)
	_, err := dev.Write(report[:])
	if err != nil {
		return "", fmt.Errorf("kraken: write serial request: %w", err)
	}
	time.Sleep(5 * time.Millisecond)

	resp := make([]byte, KrakenReportSize)
	n, err := dev.ReadWithTimeout(resp, 500)
	if err != nil || n == 0 {
		return "", fmt.Errorf("kraken: read serial response: %w", err)
	}

	if resp[0] == 0x05 {
		serial := ""
		for i := 1; i < n && i <= 22; i++ {
			if resp[i] == 0 {
				break
			}
			if resp[i] >= 30 && resp[i] <= 126 {
				serial += string(resp[i])
			}
		}
		return serial, nil
	}
	return "", nil
}
