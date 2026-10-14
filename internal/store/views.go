package store

import "context"

// mediaCount counts the media rows of canonical chat m.canonical_jid whose state is in states.
func mediaCount(states string) string {
	return `(SELECT COUNT(*) FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid WHERE COALESCE(a.canonical_jid,x.chat_jid)=m.canonical_jid AND x.state IN (` + states + `))`
}

// ChatStats counts media as failed, expired (including a re-upload request waiting for the phone) and pending
// (including a download in progress); done and skipped rows are not counted.
func (s *Store) ChatStats(ctx context.Context) ([]ChatStat, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT m.canonical_jid,COALESCE(MAX(c.name),''),COUNT(*),MIN(m.ts),MAX(m.ts),
 `+mediaCount(`'failed'`)+`,`+mediaCount(`'expired','retry_requested'`)+`,`+mediaCount(`'pending','downloading'`)+`,
 (SELECT COUNT(*) FROM open_gaps g LEFT JOIN chat_aliases a ON a.alias_jid=g.chat_jid WHERE COALESCE(a.canonical_jid,g.chat_jid)=m.canonical_jid)
 FROM messages_resolved m LEFT JOIN chats c ON c.jid=m.chat_jid GROUP BY m.canonical_jid ORDER BY m.canonical_jid`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ChatStat
	for rows.Next() {
		var v ChatStat
		if err = rows.Scan(&v.CanonicalJID, &v.Name, &v.MessageCount, &v.Oldest, &v.Newest, &v.MediaFailed, &v.MediaExpired, &v.MediaPending, &v.OpenGaps); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
