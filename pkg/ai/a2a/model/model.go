package model

import (
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	goa2a "trpc.group/trpc-go/trpc-a2a-go/protocol"
)

type Message struct {
	*goa2a.Message
}

func (m *Message) Text() string {
	for _, part := range m.Parts {
		if textPart, ok := part.(*protocol.TextPart); ok {
			return textPart.Text
		}
	}
	return ""
}
