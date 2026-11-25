package webwindow

// homeCommand runs at most one address lookup per window. Its state belongs to
// the UI thread and read runs in the background. A newer command can replace
// or cancel a request while a profile read is in progress.
type homeCommand struct {
	address  string
	read     func() string
	dispatch func(func())
	navigate func(string)
	request  uint64
	reading  bool
}

func (h *homeCommand) open(navigate func(string)) {
	h.request++
	h.navigate = navigate
	h.readPending()
}

func (h *homeCommand) cancel() { h.navigate = nil }

func (h *homeCommand) readPending() {
	if h.reading || h.navigate == nil {
		return
	}
	h.reading = true
	request := h.request
	go func() {
		address := h.read()
		h.dispatch(func() {
			h.reading = false
			if request != h.request {
				h.readPending()
				return
			}
			if h.navigate != nil {
				if address != "" {
					h.address = address
				}
				navigate := h.navigate
				h.navigate = nil
				navigate(h.address)
			}
		})
	}()
}
