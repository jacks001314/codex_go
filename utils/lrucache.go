package utils

import (
	"container/list"
	"crypto/sha1"
	"sync"
)

type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	items    map[K]*list.Element
	order    *list.List
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

func New[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity < 0 {
		capacity = 0
	}
	return &Cache[K, V]{
		capacity: capacity,
		items:    make(map[K]*list.Element),
		order:    list.New(),
	}
}

func TryWithCapacity[K comparable, V any](capacity int) *Cache[K, V] {
	if capacity <= 0 {
		return nil
	}
	return New[K, V](capacity)
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*entry[K, V]).value, true
}

func (c *Cache[K, V]) Insert(key K, value V) (V, bool) {
	var zero V
	if c == nil || c.capacity == 0 {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		old := elem.Value.(*entry[K, V]).value
		elem.Value.(*entry[K, V]).value = value
		c.order.MoveToFront(elem)
		return old, true
	}
	elem := c.order.PushFront(&entry[K, V]{key: key, value: value})
	c.items[key] = elem
	c.evictLocked()
	return zero, false
}

func (c *Cache[K, V]) GetOrInsertWith(key K, value func() V) V {
	if c == nil || c.capacity == 0 {
		return value()
	}
	if v, ok := c.Get(key); ok {
		return v
	}
	v := value()
	c.Insert(key, v)
	return v
}

func (c *Cache[K, V]) GetOrTryInsertWith(key K, value func() (V, error)) (V, error) {
	if c == nil || c.capacity == 0 {
		return value()
	}
	if v, ok := c.Get(key); ok {
		return v, nil
	}
	v, err := value()
	if err != nil {
		var zero V
		return zero, err
	}
	c.Insert(key, v)
	return v, nil
}

func (c *Cache[K, V]) Remove(key K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.order.Remove(elem)
	delete(c.items, key)
	return elem.Value.(*entry[K, V]).value, true
}

func (c *Cache[K, V]) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[K]*list.Element)
	c.order.Init()
}

func (c *Cache[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *Cache[K, V]) WithMut(callback func(map[K]V) map[K]V) {
	if c == nil || callback == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := make(map[K]V, len(c.items))
	for key, elem := range c.items {
		snapshot[key] = elem.Value.(*entry[K, V]).value
	}
	updated := callback(snapshot)
	if updated == nil {
		return
	}
	c.items = make(map[K]*list.Element)
	c.order.Init()
	for key, value := range updated {
		elem := c.order.PushFront(&entry[K, V]{key: key, value: value})
		c.items[key] = elem
		c.evictLocked()
	}
}

func (c *Cache[K, V]) evictLocked() {
	for c.capacity > 0 && len(c.items) > c.capacity {
		elem := c.order.Back()
		if elem == nil {
			return
		}
		c.order.Remove(elem)
		delete(c.items, elem.Value.(*entry[K, V]).key)
	}
}

func SHA1Digest(bytes []byte) [20]byte {
	return sha1.Sum(bytes)
}
