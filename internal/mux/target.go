package mux

// Open-target helpers that live off the main session file.

// TargetBytes is the Open host:port as bytes.
func (st *Stream) TargetBytes() []byte { return []byte(st.target) }
