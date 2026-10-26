package ingest

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"google.golang.org/protobuf/proto"

	"whatsapp-archiver/internal/store"
)

// This file is the I3 decision (Hamza, option b, 26 Oct 2026): history_spool keeps the decrypted HistorySync blob for
// seven days after import, and archive.db is backed up every night, so the blob is stripped of key material before it
// is spooled. The history handler (W5) must call StripHistorySecrets (or StripHistorySync, when it holds the parsed
// chunk) before store.SpoolReceive. The rule is stripMessage (secrets.go), the one the live path applies to raw_proto:
// message secrets and keys never reach the archive. A message's own media key stays, because the media worker needs it
// and messages.raw_proto already stores it.
//
// Residual risk: unknown protobuf fields and opaque bytes such as WebMessageInfo.futureproofData survive the strip, so a
// secret in a field newer than the pinned protos would be spooled. The bump-whatsmeow skill re-diffs the bytes fields
// against secretFieldWords on every bump.

// maxBlobBytes bounds the decompressed size of a spooled blob (a decompression bomb must not exhaust memory). Real
// chunks are some tens of MB.
const maxBlobBytes = 1 << 30

// StripHistorySecrets takes a decrypted HistorySync blob in its spool format (store.BlobZlib or store.BlobRaw), clears
// the key material of every conversation and message in it (see secretFieldWords, secretFields and the protocol-message
// payloads of dropKeyPayloads) and returns the blob in the same format. Everything else is kept, field for field, so
// the importer sees the same chunk. It is idempotent: stripping a stripped blob returns the same bytes. A blob that
// cannot be read (unknown format, bad zlib, bad protobuf, trailing data, over maxBlobBytes) is an error, and the caller
// must not spool it. The input is not modified.
func StripHistorySecrets(blob []byte, format string) ([]byte, error) {
	var raw []byte
	switch format {
	case store.BlobRaw:
		raw = blob
	case store.BlobZlib:
		var err error
		if raw, err = inflate(blob); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("strip history secrets: unknown blob format %q", format)
	}
	hs := &waHistorySync.HistorySync{}
	if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(raw, hs); err != nil {
		return nil, fmt.Errorf("strip history secrets: decode history sync: %w", err)
	}
	stripMessage(hs.ProtoReflect())
	clean, err := proto.MarshalOptions{Deterministic: true, AllowPartial: true}.Marshal(hs)
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: encode history sync: %w", err)
	}
	if format == store.BlobRaw {
		return clean, nil
	}
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err = zw.Write(clean); err != nil {
		return nil, fmt.Errorf("strip history secrets: compress: %w", err)
	}
	if err = zw.Close(); err != nil {
		return nil, fmt.Errorf("strip history secrets: compress: %w", err)
	}
	return out.Bytes(), nil
}

// StripHistorySync returns a copy of hs without key material, by the same rule as StripHistorySecrets. hs itself is not
// modified. A nil hs gives nil.
func StripHistorySync(hs *waHistorySync.HistorySync) *waHistorySync.HistorySync {
	if hs == nil {
		return nil
	}
	clean := proto.Clone(hs).(*waHistorySync.HistorySync)
	stripMessage(clean.ProtoReflect())
	return clean
}

func inflate(blob []byte) ([]byte, error) {
	src := bytes.NewReader(blob)
	zr, err := zlib.NewReader(src)
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: open zlib blob: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(zr, maxBlobBytes+1))
	if err != nil {
		return nil, fmt.Errorf("strip history secrets: read zlib blob: %w", err)
	}
	if len(raw) > maxBlobBytes {
		return nil, fmt.Errorf("strip history secrets: blob is over %d bytes decompressed", maxBlobBytes)
	}
	if src.Len() > 0 {
		return nil, fmt.Errorf("strip history secrets: %d bytes follow the zlib stream", src.Len())
	}
	return raw, nil
}
