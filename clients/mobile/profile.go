package mobile

import "github.com/a3app/loft/internal/wire"

// ProfileJSON maps a signed lists blob to SplitStore JSON (src=op).
func ProfileJSON(blob []byte) (string, error) {
	l, err := wire.DecodeLists(blob)
	if err != nil {
		return "", err
	}
	b, err := l.RouteProfile().SplitJSON()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
