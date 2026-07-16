package wgengine

import "testing"

// A representative AmneziaWG 2.0 server config as the panel generates it, with
// PostUp/DNS lines the engine must ignore and two peers.
const sampleAWGConf = `[Interface]
PrivateKey = QOlCgUZmA3z1n2h5J7kF9pR2sT4uV6wX8yZ0aB2cD4E=
Address = 10.66.66.1/24, 2a01:aaa::1/112
ListenPort = 51820
MTU = 1420
Jc = 4
Jmin = 50
Jmax = 1000
S1 = 0
S2 = 0
S3 = 20
S4 = 12
H1 = 100000-800000
H2 = 2
H3 = 3
H4 = 4
I1 = <r 128>
PostUp = iptables -A FORWARD -i awg0 -j ACCEPT; sysctl -w net.ipv4.ip_forward=1
PostDown = iptables -D FORWARD -i awg0 -j ACCEPT

[Peer]
# alice
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = FpCcVwXyZ0aB2cD4eF6gH8iJ0kL2mN4oP6qR8sT0uY=
AllowedIPs = 10.66.66.2/32, 2a01:aaa::2/128

[Peer]
# bob (disabled peers are omitted by the generator, so all present are enabled)
PublicKey = m4Rt1NAB4mZqp8DgxTIBA5rboUvnH4htodjb6e697Qk=
AllowedIPs = 10.66.66.3/32
`

func TestParseQuickConfig_AWG(t *testing.T) {
	cfg, err := ParseQuickConfig(sampleAWGConf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.ListenPort != 51820 {
		t.Errorf("ListenPort = %d, want 51820", cfg.ListenPort)
	}
	if cfg.MTU != 1420 {
		t.Errorf("MTU = %d, want 1420", cfg.MTU)
	}
	if len(cfg.Addresses) != 2 {
		t.Errorf("Addresses = %d, want 2", len(cfg.Addresses))
	}
	// AWG params captured, including 2.0 range + I1 DSL; kernel lines dropped.
	if cfg.AWG["h1"] != "100000-800000" {
		t.Errorf("h1 = %q, want 100000-800000", cfg.AWG["h1"])
	}
	if cfg.AWG["i1"] != "<r 128>" {
		t.Errorf("i1 = %q, want <r 128>", cfg.AWG["i1"])
	}
	if _, ok := cfg.AWG["postup"]; ok {
		t.Error("postup leaked into AWG params")
	}
	if len(cfg.Peers) != 2 {
		t.Fatalf("Peers = %d, want 2", len(cfg.Peers))
	}
	if cfg.Peers[0].PublicKey != "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=" {
		t.Errorf("peer0 pubkey = %q", cfg.Peers[0].PublicKey)
	}
	if cfg.Peers[0].PresharedKey == "" {
		t.Error("peer0 preshared key missing")
	}
	if len(cfg.Peers[0].AllowedIPs) != 2 {
		t.Errorf("peer0 AllowedIPs = %d, want 2", len(cfg.Peers[0].AllowedIPs))
	}
	if len(cfg.Peers[1].AllowedIPs) != 1 || cfg.Peers[1].PresharedKey != "" {
		t.Errorf("peer1 parsed wrong: %+v", cfg.Peers[1])
	}
}

// A plain WireGuard config (no obfuscation) must parse with an empty AWG map.
func TestParseQuickConfig_PlainWG(t *testing.T) {
	const conf = `[Interface]
PrivateKey = QOlCgUZmA3z1n2h5J7kF9pR2sT4uV6wX8yZ0aB2cD4E=
Address = 10.0.0.1/24
ListenPort = 51821

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
AllowedIPs = 10.0.0.2/32
`
	cfg, err := ParseQuickConfig(conf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.AWG) != 0 {
		t.Errorf("plain WG should have no AWG params, got %v", cfg.AWG)
	}
	if cfg.MTU != 1420 {
		t.Errorf("MTU default = %d, want 1420", cfg.MTU)
	}
	if len(cfg.Peers) != 1 {
		t.Errorf("Peers = %d, want 1", len(cfg.Peers))
	}
}

func TestParseQuickConfig_Errors(t *testing.T) {
	if _, err := ParseQuickConfig("[Interface]\nListenPort = 51820\n"); err == nil {
		t.Error("expected error for missing PrivateKey")
	}
	if _, err := ParseQuickConfig("[Interface]\nPrivateKey = abc\n"); err == nil {
		t.Error("expected error for missing ListenPort")
	}
}
