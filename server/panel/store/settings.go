package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings is the panel's own state besides users: the server name chosen
// in the wizard, the ACME contact, where the wizard stopped.
type Settings struct {
	Domain    string    `json:"domain,omitempty"`
	ACMEEmail string    `json:"acme_email,omitempty"`
	CertUntil time.Time `json:"cert_until,omitempty"` // public certificate expiry; zero = self-signed / none
	// PanelDomain is the panel's own name when it differs from Domain (the
	// node's), with its own certificate.
	PanelDomain    string    `json:"panel_domain,omitempty"`
	PanelCertUntil time.Time `json:"panel_cert_until,omitempty"`
	WizardDone     bool      `json:"wizard_done,omitempty"`
	// The names and check the node's and the panel's certificates were last
	// issued with (Domains may add names); empty = the domain, HTTP-01.
	NodeCertNames   []string `json:"node_cert_names,omitempty"`
	NodeCertMethod  string   `json:"node_cert_method,omitempty"`
	PanelCertNames  []string `json:"panel_cert_names,omitempty"`
	PanelCertMethod string   `json:"panel_cert_method,omitempty"`
	Update          Update   `json:"update"`
	// Sign-in: Idle is 30m | 8h | off (only Max ends a session), Max 7d | 30d.
	SessionIdle string `json:"session_idle,omitempty"`
	SessionMax  string `json:"session_max,omitempty"`
	// Lang is the default interface language: ru | en | zh.
	Lang   string `json:"lang,omitempty"`
	Notify Notify `json:"notify"`
}

// Notify is where event letters go and through which SMTP server.
type Notify struct {
	To       string `json:"to,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Security string `json:"security,omitempty"` // starttls | tls | none
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"` // never sent to the page
	From     string `json:"from,omitempty"`
	// Events, all on by default (Off lists those switched off).
	Off []string `json:"off,omitempty"`
	// What was already reported, so a letter goes once.
	SentCert   map[string]time.Time `json:"sent_cert,omitempty"` // domain → certificate expiry reported
	SentJob    string               `json:"sent_job,omitempty"`  // update job id reported
	NodeDownAt time.Time            `json:"node_down_at,omitempty"`
}

// Update is how the panel follows GitHub releases, and what it last saw.
type Update struct {
	Mode    string `json:"mode,omitempty"`    // auto | notify | off; empty = notify
	Channel string `json:"channel,omitempty"` // stable | pre; empty = stable
	Window  string `json:"window,omitempty"`  // daily | sunday | now; empty = daily (04:00–05:00 server time)

	Checked    time.Time `json:"checked,omitempty"`
	Latest     string    `json:"latest,omitempty"` // tag, e.g. v1.0.22
	LatestDate time.Time `json:"latest_date,omitempty"`
	CheckError string    `json:"check_error,omitempty"`
	// Tried is the tag an automatic update last started, so a failing
	// release is not retried every hour.
	Tried   string    `json:"tried,omitempty"`
	TriedAt time.Time `json:"tried_at,omitempty"`
}

// SettingsFile is settings.json under the data directory.
type SettingsFile struct {
	path string
	mu   sync.Mutex
}

// OpenSettings returns dir/settings.json (it may not exist yet).
func OpenSettings(dir string) *SettingsFile {
	return &SettingsFile{path: filepath.Join(dir, "settings.json")}
}

// Get reads the file; missing is the zero value.
func (f *SettingsFile) Get() (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read()
}

func (f *SettingsFile) read() (Settings, error) {
	var s Settings
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Update changes the settings with fn and writes them atomically (0600).
func (f *SettingsFile) Update(fn func(*Settings)) (Settings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, err := f.read()
	if err != nil {
		return s, err
	}
	fn(&s)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	return s, WriteAtomic(f.path, append(b, '\n'), 0o600)
}
