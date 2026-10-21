package cdn

// Cloudflare edge profile: WebSocket front (not a protocol cap).

// Cloudflare (and any edge that passes WebSocket). Protocol front is `ws`.
func Cloudflare() Profile {
	p := GenericWS()
	p.Name = "cloudflare"
	return p
}
