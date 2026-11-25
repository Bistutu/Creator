package main

import "container/list"

const recentArtworkBudget = 12 << 20

// artworkCache keeps the images used by the current frame, plus recent
// offscreen images within a pixel budget. It is owned by the UI thread.
type artworkCache struct {
	entries      map[string]*list.Element
	recent       list.List
	visible      map[string]struct{}
	bytes        int
	visibleBytes int
}

type artworkEntry struct {
	id      string
	picture *artwork
	bytes   int
}

func newArtworkCache() artworkCache {
	return artworkCache{entries: make(map[string]*list.Element), visible: make(map[string]struct{})}
}

func (c *artworkCache) get(id string) *artwork {
	if element := c.entries[id]; element != nil {
		return element.Value.(artworkEntry).picture
	}
	return nil
}

func (c *artworkCache) beginFrame() {
	clear(c.visible)
	c.visibleBytes = 0
}

func (c *artworkCache) show(id string) {
	if _, present := c.visible[id]; present {
		return
	}
	c.visible[id] = struct{}{}
	if element := c.entries[id]; element != nil {
		c.recent.MoveToFront(element)
		c.visibleBytes += element.Value.(artworkEntry).bytes
	}
}

func (c *artworkCache) set(id string, picture *artwork) {
	c.remove(id)
	if picture == nil {
		return
	}
	bounds := picture.img.Bounds()
	// Reserve room for decoded pixels, the renderer's copy and its smaller
	// levels. This also covers a 16-bit source image.
	size := 16 * bounds.Dx() * bounds.Dy()
	c.entries[id] = c.recent.PushFront(artworkEntry{id: id, picture: picture, bytes: size})
	c.bytes += size
	if _, visible := c.visible[id]; visible {
		c.visibleBytes += size
	}
	c.trim()
}

func (c *artworkCache) remove(id string) {
	if element := c.entries[id]; element != nil {
		c.discard(element)
	}
}

func (c *artworkCache) discard(element *list.Element) {
	entry := element.Value.(artworkEntry)
	if _, visible := c.visible[entry.id]; visible {
		c.visibleBytes -= entry.bytes
	}
	c.bytes -= entry.bytes
	delete(c.entries, entry.id)
	c.recent.Remove(element)
}

func (c *artworkCache) trim() {
	for element := c.recent.Back(); element != nil && c.bytes-c.visibleBytes > recentArtworkBudget; {
		previous := element.Prev()
		if _, visible := c.visible[element.Value.(artworkEntry).id]; !visible {
			c.discard(element)
		}
		element = previous
	}
}
