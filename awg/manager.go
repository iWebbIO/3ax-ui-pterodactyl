package awg

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coinman-dev/3ax-ui/v2/logger"
)

const (
	DefaultConfigDir  = "/etc/amnezia/amneziawg"
	DefaultConfigFile = "awg0.conf"
)

// PeerStatus holds runtime stats for one peer parsed from `awg show`.
type PeerStatus struct {
	PublicKey           string `json:"publicKey"`
	Endpoint            string `json:"endpoint"`
	LatestHandshake     int64  `json:"latestHandshake"` // unix timestamp
	TransferRx          int64  `json:"transferRx"`      // bytes received
	TransferTx          int64  `json:"transferTx"`      // bytes transmitted
	PersistentKeepalive int    `json:"persistentKeepalive"`
}

// WriteServerConfig writes the config string to the awg config file.
func WriteServerConfig(interfaceName string, config string) error {
	dir := DefaultConfigDir
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	fileName := interfaceName + ".conf"
	path := filepath.Join(dir, fileName)

	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// RemoveServerConfig deletes the config file for the given interface from disk.
func RemoveServerConfig(interfaceName string) {
	path := filepath.Join(DefaultConfigDir, interfaceName+".conf")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logger.Warning("Failed to remove AWG config file:", err)
	}
}

// InterfaceUp brings the AmneziaWG interface up using awg-quick.
func InterfaceUp(interfaceName string) error {
	configPath := filepath.Join(DefaultConfigDir, interfaceName+".conf")
	cmd := exec.Command("awg-quick", "up", configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg-quick up failed: %s: %w", string(output), err)
	}
	logger.Info("AmneziaWG interface", interfaceName, "is up")
	return nil
}

// InterfaceDown takes the AmneziaWG interface down.
func InterfaceDown(interfaceName string) error {
	configPath := filepath.Join(DefaultConfigDir, interfaceName+".conf")
	cmd := exec.Command("awg-quick", "down", configPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("awg-quick down failed: %s: %w", string(output), err)
	}
	logger.Info("AmneziaWG interface", interfaceName, "is down")
	return nil
}

// SyncConfig applies config changes without dropping existing connections.
func SyncConfig(interfaceName string) error {
	configPath := filepath.Join(DefaultConfigDir, interfaceName+".conf")

	// Strip the config to get only the interface section for syncconf
	cmd := exec.Command("awg-quick", "strip", configPath)
	stripped, err := cmd.Output()
	if err != nil {
		// Fallback: restart the interface
		logger.Warning("awg-quick strip failed, restarting interface:", err)
		return RestartInterface(interfaceName)
	}

	syncCmd := exec.Command("awg", "syncconf", interfaceName, "/dev/stdin")
	syncCmd.Stdin = strings.NewReader(string(stripped))
	output, err := syncCmd.CombinedOutput()
	if err != nil {
		logger.Warning("awg syncconf failed, restarting interface:", string(output), err)
		return RestartInterface(interfaceName)
	}
	return nil
}

// RestartInterface performs a full down+up cycle.
func RestartInterface(interfaceName string) error {
	// Ignore error on down (interface might not be up)
	_ = InterfaceDown(interfaceName)
	time.Sleep(500 * time.Millisecond)
	return InterfaceUp(interfaceName)
}

// IsInterfaceUp checks if the awg interface exists in the system.
func IsInterfaceUp(interfaceName string) bool {
	cmd := exec.Command("awg", "show", interfaceName)
	err := cmd.Run()
	return err == nil
}

// GetPeerStats parses `awg show <iface> dump` to get per-peer traffic stats.
// The dump format has tab-separated fields:
// Line 1 (interface): private-key listen-port fwmark
// Line 2+ (peers): public-key preshared-key endpoint allowed-ips latest-handshake transfer-rx transfer-tx persistent-keepalive
func GetPeerStats(interfaceName string) ([]PeerStatus, error) {
	cmd := exec.Command("awg", "show", interfaceName, "dump")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("awg show dump failed: %w", err)
	}

	var peers []PeerStatus
	scanner := bufio.NewScanner(strings.NewReader(string(output)))

	// Skip first line (interface info)
	if scanner.Scan() {
		// skip
	}

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}

		handshake, _ := strconv.ParseInt(fields[4], 10, 64)
		rx, _ := strconv.ParseInt(fields[5], 10, 64)
		tx, _ := strconv.ParseInt(fields[6], 10, 64)
		keepalive, _ := strconv.Atoi(fields[7])

		peers = append(peers, PeerStatus{
			PublicKey:           fields[0],
			Endpoint:            fields[2],
			LatestHandshake:     handshake,
			TransferRx:          rx,
			TransferTx:          tx,
			PersistentKeepalive: keepalive,
		})
	}

	return peers, nil
}

// IsAwgInstalled checks if the awg and awg-quick binaries are available.
func IsAwgInstalled() bool {
	_, err1 := exec.LookPath("awg")
	_, err2 := exec.LookPath("awg-quick")
	return err1 == nil && err2 == nil
}

// awg20ReleaseDate is the build date (YYYYMMDD) from which AmneziaWG speaks 2.0
// (S3/S4 padding, ranged H1-H4, I1-I5 signature packets). AmneziaWG 2.0 shipped
// 2025-09-01, so a kernel module built on/after that date is 2.0-capable.
const awg20ReleaseDate = 20250901

// GetAwgVersion returns a human-meaningful AmneziaWG version for display.
//
// The amneziawg-tools binary cosmetically reports the inherited wireguard-tools
// base version via `awg --version` (e.g. "v1.0.20210914") and never bumps it
// (upstream amneziawg-tools issue #21), which misleads users into thinking the
// install is ancient. The meaningful version is the kernel module's, exposed at
// /sys/module/amneziawg/version (e.g. "1.0.20251009"); we derive the protocol
// generation from its build date and report "2.0.<date>" / "1.x.<date>".
func GetAwgVersion() string {
	if mod := awgModuleVersion(); mod != "" {
		switch date := awgVersionDate(mod); {
		case date >= awg20ReleaseDate:
			return fmt.Sprintf("v2.0.%d", date)
		case date > 0:
			return fmt.Sprintf("v1.x.%d", date)
		default:
			return withVPrefix(mod)
		}
	}
	// Fallback: the (cosmetic) tools version string — only when the module
	// version can't be read (e.g. module not loaded yet).
	output, err := exec.Command("awg", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return withVPrefix(strings.TrimSpace(string(output)))
}

// withVPrefix prefixes a version string with "v" for display, unless it already
// has one or is empty.
func withVPrefix(v string) string {
	if v == "" || strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return v
	}
	return "v" + v
}

// awgModuleVersion returns the AmneziaWG kernel module version (e.g. "1.0.20251009"),
// preferring the loaded module's sysfs entry and falling back to modinfo.
func awgModuleVersion() string {
	if b, err := os.ReadFile("/sys/module/amneziawg/version"); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	if out, err := exec.Command("modinfo", "-F", "version", "amneziawg").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// awgVersionDate extracts the YYYYMMDD build date from a version string such as
// "1.0.20251009" (the amneziawg/wireguard-tools versioning scheme). Returns 0
// when no plausible date is present.
func awgVersionDate(v string) int {
	for i := 0; i+8 <= len(v); i++ {
		seg := v[i : i+8]
		if seg[0] != '2' || seg[1] != '0' {
			continue
		}
		if n, err := strconv.Atoi(seg); err == nil {
			return n
		}
	}
	return 0
}
