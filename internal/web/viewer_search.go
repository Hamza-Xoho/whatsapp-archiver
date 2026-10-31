package web

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"whatsapp-archiver/internal/store"
)

func (s *Server) viewerSearch(ctx context.Context, jid string, values url.Values, q *store.ViewerQuery) (store.ChatSearchPosition, error) {
	text := strings.TrimSpace(values.Get("q"))
	if text == "" {
		return store.ChatSearchPosition{}, nil
	}
	index := 0
	if raw := values.Get("match"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return store.ChatSearchPosition{}, fmt.Errorf("match is out of range")
		}
		index = n
	}
	position, err := s.d.Store.SearchChatPosition(ctx, jid, text, *q, index)
	if err != nil || position.Count == 0 {
		return position, err
	}
	q.FocusID = position.CurrentID
	return position, nil
}
