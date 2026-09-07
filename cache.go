package main

import "container/list"

// boundedCache is accessed under the relay mutex; entries are evicted in LRU order.
type boundedCache[K comparable, V any] struct {
	capacity int
	items    map[K]*list.Element
	order    *list.List
}
type cacheEntry[K comparable, V any] struct {
	key   K
	value V
}

func newCache[K comparable, V any](capacity int) *boundedCache[K, V] {
	return &boundedCache[K, V]{capacity, make(map[K]*list.Element), list.New()}
}
func (c *boundedCache[K, V]) get(k K) (V, bool) {
	if e, ok := c.items[k]; ok {
		c.order.MoveToFront(e)
		return e.Value.(cacheEntry[K, V]).value, true
	}
	var v V
	return v, false
}
func (c *boundedCache[K, V]) put(k K, v V) {
	if e, ok := c.items[k]; ok {
		e.Value = cacheEntry[K, V]{k, v}
		c.order.MoveToFront(e)
		return
	}
	c.items[k] = c.order.PushFront(cacheEntry[K, V]{k, v})
	if len(c.items) > c.capacity {
		e := c.order.Back()
		delete(c.items, e.Value.(cacheEntry[K, V]).key)
		c.order.Remove(e)
	}
}
