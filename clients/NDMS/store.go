package ndms

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// File is /opt/etc/loft/client.yaml: the panel listen address, Keenetic
// endpoints, and one record per connection.
type File struct {
	Listen struct {
		HTTP string `yaml:"http" json:"http"`
	} `yaml:"listen" json:"listen"`
	Keenetic struct {
		Auth         string `yaml:"auth" json:"auth"`
		ISP          string `yaml:"isp,omitempty" json:"isp,omitempty"`
		RCI          string `yaml:"rci" json:"rci"`
		CheckUpdates bool   `yaml:"check_updates" json:"check_updates"`
	} `yaml:"keenetic" json:"keenetic"`
	Connections []Conn `yaml:"connections" json:"connections"`
}

// Conn is one OpkgTun. Mode is link, manual, or file (an imported profile
// the editor has not saved over yet).
type Conn struct {
	ID      string `yaml:"id" json:"id"`
	Name    string `yaml:"name" json:"name"` // ndmc description, at most 32 runes
	Enabled bool   `yaml:"enabled" json:"enabled"`
	Opkg    int    `yaml:"opkg" json:"opkg"` // 0..15; -1 asks Put for the next free index
	Kind    string `yaml:"kind" json:"kind"` // loft, vless or trojan
	Mode    string `yaml:"mode" json:"mode"` // link, manual or file
	Link    string `yaml:"link,omitempty" json:"link,omitempty"`
	// Profile is an imported YAML document. The editor clears it on save
	// so the fields below become the source.
	Profile  string   `yaml:"profile,omitempty" json:"profile,omitempty"`
	Token    string   `yaml:"token,omitempty" json:"token,omitempty"`
	Host     string   `yaml:"host,omitempty" json:"host,omitempty"`
	Port     int      `yaml:"port,omitempty" json:"port,omitempty"`
	Path     string   `yaml:"path,omitempty" json:"path,omitempty"`
	Connect  string   `yaml:"connect,omitempty" json:"connect,omitempty"` // front shown before the session is up
	Fronts   []string `yaml:"fronts,omitempty" json:"fronts,omitempty"`
	CC       string   `yaml:"cc,omitempty" json:"cc,omitempty"` // off, bbr or fixed; TCP fronts stay off
	CCUp     int      `yaml:"cc_up_mbps,omitempty" json:"cc_up_mbps,omitempty"`
	CCDown   int      `yaml:"cc_down_mbps,omitempty" json:"cc_down_mbps,omitempty"`
	UUID     string   `yaml:"uuid,omitempty" json:"uuid,omitempty"`
	Password string   `yaml:"password,omitempty" json:"password,omitempty"`
	Security string   `yaml:"security,omitempty" json:"security,omitempty"` // tls, reality or none
	Network  string   `yaml:"network,omitempty" json:"network,omitempty"`   // tcp or xhttp
	SNI      string   `yaml:"sni,omitempty" json:"sni,omitempty"`
	FP       string   `yaml:"fp,omitempty" json:"fp,omitempty"`
	PBK      string   `yaml:"public_key,omitempty" json:"public_key,omitempty"`
	SID      string   `yaml:"short_id,omitempty" json:"short_id,omitempty"`
	Vision   bool     `yaml:"vision,omitempty" json:"vision,omitempty"` // vless+tcp only
}

// norm fills listen, auth and RCI when the YAML omits them.
// CheckUpdates stays false when absent: the sample sets it explicitly,
// and a missing bool must not turn the GitHub check on.
func (f *File) norm() {
	if f.Listen.HTTP == "" {
		f.Listen.HTTP = ":8090"
	}
	if f.Keenetic.Auth == "" {
		f.Keenetic.Auth = "http://127.0.0.1/auth"
	}
	if f.Keenetic.RCI == "" {
		f.Keenetic.RCI = "http://127.0.0.1:79/rci/show/ip/route"
	}
	if f.Connections == nil {
		f.Connections = []Conn{}
	}
}

// Store is the YAML file. Mutations hold the lock for the whole read-modify-write.
type Store struct {
	mu   sync.Mutex
	path string
}

// OpenStore is the on-disk config. The file must already exist.
func OpenStore(path string) *Store { return &Store{path: path} }

// Path is the YAML path.
func (s *Store) Path() string { return s.path }

// Load reads and fills defaults. The returned value is a fresh decode.
func (s *Store) Load() (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// read decodes the file and applies norm. The caller holds s.mu.
func (s *Store) read() (File, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return File{}, err
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return File{}, err
	}
	f.norm()
	return f, nil
}

// write replaces the file via a sibling temp and rename, mode 0600.
// The YAML holds member keys and proxy passwords.
func (s *Store) write(f File) error {
	f.norm()
	b, err := yaml.Marshal(&f)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Update reads, lets fn change the file, and writes it back.
func (s *Store) Update(fn func(*File) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(&f); err != nil {
		return err
	}
	return s.write(f)
}

// idRe is the connection id: one file name under the state directory,
// and one argument of -session. A slash or ".." cannot match.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// newID is eight hex characters from four random bytes.
func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Put inserts or replaces a connection. Opkg < 0 takes the next free index.
// Empty secrets on an existing id keep the stored value. A save from the
// editor clears Profile so the fields win.
func (s *Store) Put(c Conn, fromEditor bool) (Conn, error) {
	var out Conn
	err := s.Update(func(f *File) error {
		c.Kind = strings.ToLower(strings.TrimSpace(c.Kind))
		c.Mode = strings.ToLower(strings.TrimSpace(c.Mode))
		if c.Mode == "" {
			c.Mode = "manual"
		}
		if c.Kind != "loft" && c.Kind != "vless" && c.Kind != "trojan" {
			return fmt.Errorf("kind")
		}
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			return fmt.Errorf("name")
		}
		if _, err := quoteDesc(c.Name); err != nil {
			return err
		}
		if c.ID == "" {
			c.ID = newID()
		}
		if !idRe.MatchString(c.ID) {
			return fmt.Errorf("id")
		}
		idx := -1
		for i := range f.Connections {
			if f.Connections[i].ID == c.ID {
				idx = i
				break
			}
		}
		if idx >= 0 {
			old := f.Connections[idx]
			c = keepSecrets(old, c)
			if fromEditor {
				c.Profile = ""
			} else if c.Profile == "" {
				c.Profile = old.Profile
			}
			if c.Opkg < 0 {
				c.Opkg = old.Opkg
			}
		}
		if c.Opkg < 0 {
			n, err := nextOpkg(f.Connections, c.ID)
			if err != nil {
				return err
			}
			c.Opkg = n
		}
		if _, _, err := Names(c.Opkg); err != nil {
			return err
		}
		for _, o := range f.Connections {
			if o.ID != c.ID && o.Opkg == c.Opkg {
				return fmt.Errorf("opkg %d already used", c.Opkg)
			}
		}
		if idx >= 0 {
			f.Connections[idx] = c
		} else {
			f.Connections = append(f.Connections, c)
		}
		out = c
		return nil
	})
	return out, err
}

// keepSecrets copies token, password, uuid, public key and link from old
// when the posted field is empty. The editor does not echo a secret it
// was not given again, and an empty box means "leave it".
func keepSecrets(old, c Conn) Conn {
	if strings.TrimSpace(c.Token) == "" {
		c.Token = old.Token
	}
	if strings.TrimSpace(c.Password) == "" {
		c.Password = old.Password
	}
	if strings.TrimSpace(c.UUID) == "" {
		c.UUID = old.UUID
	}
	if strings.TrimSpace(c.PBK) == "" {
		c.PBK = old.PBK
	}
	if strings.TrimSpace(c.Link) == "" {
		c.Link = old.Link
	}
	return c
}

// nextOpkg is the lowest free index in 0..OpkgMax. self is ignored so an
// update of the same connection does not count its own index as taken.
func nextOpkg(list []Conn, self string) (int, error) {
	used := map[int]bool{}
	for _, c := range list {
		if c.ID == self {
			continue
		}
		used[c.Opkg] = true
	}
	for i := 0; i <= OpkgMax; i++ {
		if !used[i] {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no free OpkgTun")
}

// Delete removes id and returns the record that was stored.
func (s *Store) Delete(id string) (Conn, error) {
	var old Conn
	err := s.Update(func(f *File) error {
		var keep []Conn
		found := false
		for _, c := range f.Connections {
			if c.ID == id {
				old = c
				found = true
				continue
			}
			keep = append(keep, c)
		}
		if !found {
			return fmt.Errorf("not found")
		}
		f.Connections = keep
		return nil
	})
	return old, err
}

// SetAuto stores the "check for updates on its own" switch.
func (s *Store) SetAuto(on bool) error {
	return s.Update(func(f *File) error {
		f.Keenetic.CheckUpdates = on
		return nil
	})
}
