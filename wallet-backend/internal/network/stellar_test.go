package network

import "testing"

func TestResolveAsset_Native(t *testing.T) {
	for _, code := range []string{"", "XLM", "native", "NATIVE"} {
		asset, err := ResolveAsset(code, "")
		if err != nil {
			t.Fatalf("ResolveAsset(%q, \"\") returned error: %v", code, err)
		}
		if !asset.IsNative() {
			t.Fatalf("ResolveAsset(%q, \"\") = %#v, want native asset", code, asset)
		}
	}
}

func TestResolveAsset_Credit(t *testing.T) {
	issuer := "GBTNUZDIUMWZEGTNQCL5F73PIABCBJ4YQA2VJS7HXTBRDDSTWCE6UNXE"
	asset, err := ResolveAsset("USDB", issuer)
	if err != nil {
		t.Fatalf("ResolveAsset returned error: %v", err)
	}
	if asset.IsNative() {
		t.Fatal("expected a credit asset, got native")
	}
	if asset.GetCode() != "USDB" || asset.GetIssuer() != issuer {
		t.Fatalf("unexpected asset: code=%s issuer=%s", asset.GetCode(), asset.GetIssuer())
	}
}

func TestResolveAsset_MissingIssuer(t *testing.T) {
	if _, err := ResolveAsset("USDB", ""); err == nil {
		t.Fatal("expected an error for a non-native asset with no issuer")
	}
}

func TestNewClient_Testnet(t *testing.T) {
	c := NewClient("", "testnet")
	if c.NetworkPassphrase == "" {
		t.Fatal("expected a non-empty network passphrase")
	}
	if c.Horizon == nil {
		t.Fatal("expected a Horizon client")
	}
}
