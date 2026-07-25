package acp

import (
	"path/filepath"
	"testing"
)

func proxyEntry(name, proxy string) ProxyEntry {
	return ProxyEntry{Name: name, NetworkSettings: NetworkSettings{Proxy: proxy}}
}

func TestAddUpdateRemoveProxyPersists(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m := agentManager(t, cfgPath)

	if _, err := m.AddProxy(proxyEntry("office", " http://proxy.example.com:8080 ")); err != nil {
		t.Fatal(err)
	}
	if got := m.Proxies()[0].Proxy; got != "http://proxy.example.com:8080" {
		t.Errorf("proxy not trimmed: %q", got)
	}

	// Empty name, empty settings and duplicates are rejected.
	if _, err := m.AddProxy(proxyEntry("", "http://x:1")); err == nil {
		t.Error("empty name accepted")
	}
	if _, err := m.AddProxy(proxyEntry("blank", "")); err == nil {
		t.Error("an entry with no settings at all should be rejected")
	}
	if _, err := m.AddProxy(proxyEntry("OFFICE", "http://other:8080")); err == nil {
		t.Error("duplicate name accepted")
	}
	// A bare host:port is silently ignored by the CLIs, so reject it here.
	if _, err := m.AddProxy(proxyEntry("noscheme", "proxy.example.com:8080")); err == nil {
		t.Error("a proxy without a scheme should be rejected")
	}

	loaded, err := LoadConfig(cfgPath, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Proxies) != 1 || loaded.Proxies[0].Name != "office" {
		t.Fatalf("proxy not persisted: %+v", loaded.Proxies)
	}

	updated := proxyEntry("office", "http://proxy.example.com:3128")
	updated.NoProxy = "localhost,.internal"
	updated.CACert = "/etc/ssl/my-ca.pem"
	if _, err := m.UpdateProxy(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateProxy(proxyEntry("missing", "http://x:1")); err == nil {
		t.Error("updating a missing entry should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if loaded.Proxies[0].NoProxy != "localhost,.internal" || loaded.Proxies[0].CACert != "/etc/ssl/my-ca.pem" {
		t.Fatalf("update not persisted: %+v", loaded.Proxies)
	}

	if err := m.RemoveProxy("office"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveProxy("office"); err == nil {
		t.Error("removing a missing entry should fail")
	}
	loaded, _ = LoadConfig(cfgPath, t.TempDir())
	if len(loaded.Proxies) != 0 {
		t.Fatalf("removal not persisted: %+v", loaded.Proxies)
	}
}

func TestAgentReferencesProxyByName(t *testing.T) {
	m := agentManager(t, "")
	if _, err := m.AddProxy(proxyEntry("office", "http://proxy.example.com:8080")); err != nil {
		t.Fatal(err)
	}

	// An unknown configuration is rejected at save time, not at session start.
	if _, err := m.AddAgent(AgentEntry{Name: "a1", Kind: "claude", ProxyName: "nope"}); err == nil {
		t.Error("an agent naming a missing proxy config should be rejected")
	}
	if _, err := m.AddAgent(AgentEntry{Name: "a1", Kind: "claude", ProxyName: " office "}); err != nil {
		t.Fatal(err)
	}

	// Two agents share one configuration; both resolve to its settings.
	if _, err := m.AddAgent(AgentEntry{Name: "a2", Kind: "codex", ProxyName: "OFFICE"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Agents() {
		net, err := m.resolveNetwork(a)
		if err != nil {
			t.Fatalf("resolve %s: %v", a.Name, err)
		}
		if net.Proxy != "http://proxy.example.com:8080" {
			t.Errorf("agent %s resolved to %q", a.Name, net.Proxy)
		}
	}

	// An agent naming nothing runs with the app's own environment.
	net, err := m.resolveNetwork(AgentEntry{Name: "plain", Kind: "claude"})
	if err != nil || !net.IsZero() {
		t.Errorf("no proxy name should resolve to empty settings, got %+v (%v)", net, err)
	}

	// The registry entry is in use, so removing it must not silently unlink it.
	if err := m.RemoveProxy("office"); err == nil {
		t.Error("removing a referenced configuration should be refused")
	}
}

func TestProxyKindCompatibility(t *testing.T) {
	m := agentManager(t, "")
	if _, err := m.AddProxy(proxyEntry("socks", "socks5://127.0.0.1:1080")); err != nil {
		t.Fatal(err) // kind-agnostic in the registry
	}

	// Claude Code documents no SOCKS support, so the pairing is rejected.
	if _, err := m.AddAgent(AgentEntry{Name: "c", Kind: "claude", ProxyName: "socks"}); err == nil {
		t.Error("a SOCKS config on a claude agent should be rejected")
	}
	if _, err := m.AddAgent(AgentEntry{Name: "x", Kind: "codex", ProxyName: "socks"}); err != nil {
		t.Errorf("a SOCKS config on a codex agent should be accepted: %v", err)
	}

	// Editing a configuration cannot break an agent already using it either.
	if _, err := m.AddProxy(proxyEntry("office", "http://proxy.example.com:8080")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddAgent(AgentEntry{Name: "c2", Kind: "claude", ProxyName: "office"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateProxy(proxyEntry("office", "socks5://127.0.0.1:1080")); err == nil {
		t.Error("switching a config used by a claude agent to SOCKS should be refused")
	}
}
