package store

import "context"

// chatStatsQuery is the ChatStats query. Each aggregate is computed in one pass over its table and joined per chat.
// A correlated subquery per chat would rescan media, open gaps and the alias view for every chat, so the cost of
// `archiver status` would grow with chats times archive size. message_keys holds the (canonical chat, id) pairs that
// messages_resolved keeps, so the open-gap check does not rerun the window function of messages_resolved per event.
var chatStatsQuery = `WITH messages_by_chat AS (
 SELECT m.canonical_jid,COALESCE(MAX(c.name),'') AS name,COUNT(*) AS messages,MIN(m.ts) AS oldest,MAX(m.ts) AS newest
 FROM messages_resolved m LEFT JOIN chats c ON c.jid=m.chat_jid GROUP BY m.canonical_jid
),
media_by_chat AS (
 SELECT COALESCE(a.canonical_jid,x.chat_jid) AS canonical_jid,
  SUM(x.state='failed') AS failed,
  SUM(x.state IN ('expired','retry_requested')) AS expired,
  SUM(x.state IN ('pending','downloading')) AS pending
 FROM media x LEFT JOIN chat_aliases a ON a.alias_jid=x.chat_jid
 WHERE x.state IN ('failed','expired','retry_requested','pending','downloading')
 GROUP BY 1
),
message_keys AS (
 SELECT DISTINCT COALESCE(a.canonical_jid,m.chat_jid) AS canonical_jid,m.id
 FROM messages m LEFT JOIN chat_aliases a ON a.alias_jid=m.chat_jid
),
gaps_by_chat AS (
 SELECT COALESCE(a.canonical_jid,g.chat_jid) AS canonical_jid,COUNT(*) AS gaps
 FROM (SELECT e.* FROM message_events e LEFT JOIN chat_aliases ea ON ea.alias_jid=e.chat_jid
  WHERE e.kind='undecryptable' AND NOT EXISTS (SELECT 1 FROM message_keys k WHERE k.canonical_jid=COALESCE(ea.canonical_jid,e.chat_jid) AND k.id=e.target_id)) g
 LEFT JOIN chat_aliases a ON a.alias_jid=g.chat_jid
 GROUP BY 1
)
SELECT b.canonical_jid,b.name,b.messages,b.oldest,b.newest,
 COALESCE(md.failed,0),COALESCE(md.expired,0),COALESCE(md.pending,0),COALESCE(g.gaps,0)
 FROM messages_by_chat b
 LEFT JOIN media_by_chat md ON md.canonical_jid=b.canonical_jid
 LEFT JOIN gaps_by_chat g ON g.canonical_jid=b.canonical_jid
 ORDER BY b.canonical_jid`

// ChatStats counts media as failed, expired (including a re-upload request waiting for the phone) and pending
// (including a download in progress); done and skipped rows are not counted.
func (s *Store) ChatStats(ctx context.Context) ([]ChatStat, error) {
	rows, err := s.read.QueryContext(ctx, chatStatsQuery)
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

// StatusReport is everything `archiver status` reads from the archive.
type StatusReport struct {
	Chats    []ChatStat
	Coverage Coverage
}

// statusReportQuery is a stub: it reruns the two old queries until the one-pass query replaces it.
var statusReportQuery = chatStatsQuery

// StatusReport runs ChatStats and HistoryCoverage.
func (s *Store) StatusReport(ctx context.Context) (StatusReport, error) {
	var r StatusReport
	var err error
	if r.Chats, err = s.ChatStats(ctx); err != nil {
		return r, err
	}
	r.Coverage, err = s.HistoryCoverage(ctx)
	return r, err
}
