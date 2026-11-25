package icons

import (
	"context"
	"image"
	"net/http"
	"net/url"
	"time"
)

// The caller owns the candidate order and decodes one response at a time.
// Workers only download bytes. Each has room to deliver its result on exit.
type artworkProbes struct {
	ctx      context.Context
	client   *http.Client
	resolver *Resolver
	seen     map[string]bool
	results  chan []byte
	pending  int
	small    image.Image
}

func (p *artworkProbes) start(c candidate) {
	key := c.key()
	if p.ctx.Err() != nil || len(p.seen) >= maxIconCandidates || p.seen[key] {
		return
	}
	if p.results == nil {
		p.results = make(chan []byte, maxIconCandidates)
	}
	p.seen[key] = true
	p.pending++
	go func() { p.results <- artworkBytes(p.ctx, p.client, c) }()
}

func (p *artworkProbes) startPreferred(candidates []candidate) image.Image {
	p.start(candidates[0])
	if len(candidates) == 2 {
		if img := p.waitLead(); img != nil {
			return img
		}
		p.start(candidates[1])
	}
	return nil
}

func (p *artworkProbes) waitLead() image.Image {
	if p.pending == 0 {
		return nil
	}
	timer := time.NewTimer(preferredIconLead)
	defer timer.Stop()
	return p.wait(timer.C)
}

func (p *artworkProbes) wait(until <-chan time.Time) image.Image {
	for p.pending > 0 {
		select {
		case data := <-p.results:
			if img := p.accept(data); img != nil {
				return img
			}
		case <-until:
			return nil
		case <-p.ctx.Done():
			return nil
		}
	}
	return nil
}

func (p *artworkProbes) accept(data []byte) image.Image {
	p.pending--
	img := p.resolver.decodeArtwork(data)
	if artworkSide(img) >= Size {
		return img
	}
	if artworkSide(img) > artworkSide(p.small) {
		p.small = img
	}
	return nil
}

func (p *artworkProbes) readManifest(at *url.URL, allow func(*url.URL) bool) (siteManifest, image.Image) {
	read := func() siteManifest {
		data, base, err := fetch(p.ctx, p.client, at, 64<<10)
		if err != nil {
			return siteManifest{}
		}
		return parseManifest(data, base, allow)
	}
	if p.pending == 0 {
		return read(), nil
	}
	result := make(chan siteManifest, 1)
	go func() { result <- read() }()
	for {
		select {
		case data := <-p.results:
			if img := p.accept(data); img != nil {
				return siteManifest{}, img
			}
		case site := <-result:
			return site, nil
		case <-p.ctx.Done():
			return siteManifest{}, nil
		}
	}
}
