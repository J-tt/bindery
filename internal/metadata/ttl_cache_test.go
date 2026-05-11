package metadata

import (
	"testing"
	"time"
)

func TestTTLCache_SetAndGet(t *testing.T) {
	c := newTTLCache(time.Minute)
	c.set("key1", "value1")
	v, ok := c.get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if v.(string) != "value1" {
		t.Errorf("want 'value1', got %q", v)
	}
}

func TestTTLCache_Miss(t *testing.T) {
	c := newTTLCache(time.Minute)
	_, ok := c.get("missing")
	if ok {
		t.Error("expected cache miss for unknown key")
	}
}

func TestTTLCache_Expiry(t *testing.T) {
	c := newTTLCache(time.Nanosecond)
	c.set("k", "v")
	time.Sleep(2 * time.Millisecond)
	_, ok := c.get("k")
	if ok {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestTTLCache_Cleanup(t *testing.T) {
	c := newTTLCache(time.Nanosecond)
	c.set("a", 1)
	c.set("b", 2)
	time.Sleep(2 * time.Millisecond)
	c.cleanup()

	c.mu.RLock()
	n := len(c.items)
	c.mu.RUnlock()
	if n != 0 {
		t.Errorf("expected 0 items after cleanup, got %d", n)
	}
}
