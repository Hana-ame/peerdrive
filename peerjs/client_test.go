package peerjs

import (
	"encoding/json"
	"testing"
)

func TestEndpointPath(t *testing.T) {
	cases := []struct {
		cfg  Config
		want string
	}{
		{Config{}, "/peerjs"},
		{Config{Path: "/", Key: "peerjs"}, "/peerjs"},
		{Config{Path: "/app", Key: "k"}, "/app/k"},
		{Config{Path: "app/", Key: "k"}, "/app/k"},
	}
	for _, c := range cases {
		if got := endpointPath(withDefaults(c.cfg)); got != c.want {
			t.Errorf("endpointPath(%+v) = %q, want %q", c.cfg, got, c.want)
		}
	}
}

func TestWSURL(t *testing.T) {
	got := wsURL(withDefaults(Config{}), "abc-123", "tok")
	want := "wss://0.peerjs.com:443/peerjs?key=peerjs&id=abc-123&token=tok"
	if got != want {
		t.Fatalf("wsURL = %q, want %q", got, want)
	}
}

func TestEncodeDecodeText(t *testing.T) {
	for _, s := range []string{"hello", "中文消息", "", `quote"inside`, "line\nbreak", "多行\n第二行"} {
		if got := decodeText([]byte(encodeText(s))); got != s {
			t.Errorf("round trip %q → %q", s, got)
		}
	}
}

func TestOfferPayloadParse(t *testing.T) {
	raw := `{"dst":"go-id","payload":{"connectionId":"dc_bt8nhy9fngc","label":"dc_bt8nhy9fngc","reliable":false,"sdp":{"sdp":"v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\n","type":"offer"},"serialization":"json","type":"data"},"src":"browser-id","type":"OFFER"}`

	var m message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if m.Type != "OFFER" || m.Src != "browser-id" {
		t.Fatalf("unexpected envelope: %+v", m)
	}

	var p offerPayload
	if err := json.Unmarshal(m.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.ConnectionID != "dc_bt8nhy9fngc" {
		t.Errorf("connectionId = %q", p.ConnectionID)
	}
	if p.Serialization != SerializationJSON {
		t.Errorf("serialization = %q", p.Serialization)
	}
	if p.SDP.Type != "offer" || p.SDP.SDP == "" {
		t.Errorf("sdp = %+v", p.SDP)
	}
}

func TestCandidatePayloadParse(t *testing.T) {
	raw := `{"connectionId":"dc_1","candidate":{"candidate":"candidate:1 1 udp 2113 192.168.1.2 5000 typ host","sdpMLineIndex":0,"sdpMid":"0","usernameFragment":"abcd"}}`

	var p candidatePayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("unmarshal candidate: %v", err)
	}
	if p.ConnectionID != "dc_1" {
		t.Errorf("connectionId = %q", p.ConnectionID)
	}
	if p.Candidate.SDPMid == nil || *p.Candidate.SDPMid != "0" {
		t.Errorf("sdpMid = %v", p.Candidate.SDPMid)
	}
	if p.Candidate.SDPMLineIndex == nil || *p.Candidate.SDPMLineIndex != 0 {
		t.Errorf("sdpMLineIndex = %v", p.Candidate.SDPMLineIndex)
	}
}

func TestWithDefaults(t *testing.T) {
	cfg := withDefaults(Config{})
	if cfg.Host != DefaultHost || cfg.Port != DefaultPort || cfg.Path != DefaultPath || cfg.Key != DefaultKey {
		t.Fatalf("defaults = %+v", cfg)
	}
	if len(cfg.ICEServers) == 0 {
		t.Fatal("expected default ICE servers")
	}
}

func TestRandomUUIDShape(t *testing.T) {
	id := randomUUID()
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("bad uuid: %q", id)
	}
}
