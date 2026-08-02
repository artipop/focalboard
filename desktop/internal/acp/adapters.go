package acp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Two of the kinds we support are reached through an adapter their vendor
// publishes on npm and nowhere else, which makes "is this agent usable on this
// machine" a question with three answers rather than one: the adapter is
// installed, it is not but npx can fetch it, or there is no Node.js at all and
// nothing can be done from here.
//
// Answering it in the agents dialog is the point. A missing adapter used to
// surface as a session that failed at its first turn, on a card, minutes after
// somebody configured the agent — while the fact was knowable the moment the
// dialog was opened.

// AdapterStatus is what the UI shows next to a kind.
type AdapterStatus struct {
	Kind string `json:"kind"`
	// Package is the npm package that provides the adapter, empty for a kind
	// whose CLI is installed some other way.
	Package string `json:"package,omitempty"`
	// Path is the adapter binary we found, empty when there is none.
	Path string `json:"path,omitempty"`
	// Ready reports that a session of this kind can start right now.
	Ready bool `json:"ready"`
	// ViaNPX marks a kind that is not installed but will be run through npx —
	// it works, only the first run pays for the download.
	ViaNPX bool `json:"viaNpx,omitempty"`
	// Detail says what is missing, in the words the user needs to act on.
	Detail string `json:"detail,omitempty"`
}

// AdapterStatuses reports every kind we know how to launch, in the order the UI
// offers them. The generic acp kind is absent: it carries its own command, so
// there is nothing to check.
func (m *Manager) AdapterStatuses() []AdapterStatus {
	out := make([]AdapterStatus, 0, len(AgentKinds))
	for _, kind := range AgentKinds {
		if !knownAdapter(kind) {
			continue
		}
		out = append(out, adapterStatus(kind))
	}
	return out
}

func adapterStatus(kind string) AdapterStatus {
	def := acpNative[kind]
	st := AdapterStatus{Kind: kind, Package: def.npmPackage}
	if bin, err := lookupBin(def.bin, ""); err == nil {
		st.Path, st.Ready = bin, true
		return st
	}
	if def.npmPackage == "" {
		st.Detail = fmt.Sprintf("не найден %s — укажите binPath или command у агента", def.bin)
		return st
	}
	if _, err := lookupBin("npx", ""); err == nil {
		st.Ready, st.ViaNPX = true, true
		st.Detail = fmt.Sprintf("%s не установлен — будет запускаться через npx (первый запуск дольше)", def.bin)
		return st
	}
	// Nothing to offer: npm is how both adapters are published, and installing
	// Node.js is not something to do behind the user's back.
	st.Detail = fmt.Sprintf("не найден ни %s, ни npx — поставьте Node.js и выполните `npm install -g %s`", def.bin, def.npmPackage)
	return st
}

// installTimeout bounds an adapter install. Both adapters pull a CLI of their
// own along with them, so a slow link needs real time here.
const installTimeout = 10 * time.Minute

// InstallAdapter installs the kind's adapter globally with npm. It is the
// dialog's "install" button and nothing more: the same command the user would
// type, run for them, with npm's own output returned so a failure is readable
// rather than a spinner that stops.
func (m *Manager) InstallAdapter(kind string) (string, error) {
	def, known := acpNative[kind]
	if !known || def.npmPackage == "" {
		return "", fmt.Errorf("для агента %q нечего устанавливать", kind)
	}
	npm, err := lookupBin("npm", "")
	if err != nil {
		return "", fmt.Errorf("не найден npm — установите Node.js")
	}
	parent := m.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, installTimeout)
	defer cancel()

	m.log.Info("acp: installing agent adapter", "kind", kind, "package", def.npmPackage)
	cmd := exec.CommandContext(ctx, npm, "install", "-g", def.npmPackage)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		return text, fmt.Errorf("npm install -g %s: %w", def.npmPackage, err)
	}
	m.log.Info("acp: agent adapter installed", "kind", kind, "package", def.npmPackage)
	return text, nil
}
