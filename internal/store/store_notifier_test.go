package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestNotifierConfigRoundTrip(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes

	blob := `{"dingtalk":{"webhook":"https://oapi.dingtalk.com/x","secret":"SEC1"},"lark":{"webhook":"https://open.feishu.cn/y","secret":""},"qq":{"app_id":"a","app_secret":"b","group_openid":"c"}}`

	// Missing → sql.ErrNoRows.
	if _, err := s.GetNotifierConfig(ctx, NotifierConfigID, key); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("want sql.ErrNoRows got %v", err)
	}

	if err := s.SetNotifierConfig(ctx, NotifierConfigID, key, blob); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := s.GetNotifierConfig(ctx, NotifierConfigID, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != blob {
		t.Errorf("round-trip mismatch: got %q want %q", got, blob)
	}

	// Upsert (overwrite).
	if err := s.SetNotifierConfig(ctx, NotifierConfigID, key, `{"qq":{"app_id":"a2"}}`); err != nil {
		t.Fatalf("set2: %v", err)
	}
	got2, _ := s.GetNotifierConfig(ctx, NotifierConfigID, key)
	if got2 != `{"qq":{"app_id":"a2"}}` {
		t.Errorf("overwrite mismatch: %q", got2)
	}
}

func TestNotifierConfigEncryptionDisabled(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := s.SetNotifierConfig(ctx, NotifierConfigID, nil, `{}`); !errors.Is(err, ErrEncryptionDisabled) {
		t.Errorf("set with nil key: want ErrEncryptionDisabled got %v", err)
	}
	if _, err := s.GetNotifierConfig(ctx, NotifierConfigID, nil); !errors.Is(err, ErrEncryptionDisabled) {
		t.Errorf("get with nil key: want ErrEncryptionDisabled got %v", err)
	}
}
