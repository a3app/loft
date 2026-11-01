package wire

import "time"

// Edge is the GET preset a node advertises in Status after the handshake.
// 0 is plain: the client keeps the limits it opened the first GET with.
// Vendor names are not on the wire.
//
// The "edge" is the CDN or reverse proxy in front of the node. Some edges
// drop the connection when the client sends many large-header GETs at once,
// so the node tells the client to use smaller, gentler limits ("tight").
const (
	EdgePlain byte = 0
	EdgeTight byte = 1 // GET edge that drops a fat concurrent Cookie burst
)

// Tight GET preset. The handshake itself stays one GET under 16 KiB.
// The pump applies these numbers before the first burst.
const (
	EdgeTightBudget   uint16 = 16 << 10
	EdgeTightInflight uint8  = 16
)

// EdgeTightTrip is one data GET. A tight edge answers or fails inside this.
const EdgeTightTrip = 6 * time.Second
