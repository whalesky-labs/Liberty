package client

// NavigationEntry keeps the page identity separate from the active processing
// mode, so revisiting a detail never substitutes another source's same-ID job.
type NavigationEntry struct {
	Path, DetailSource, SettingsSection string
}

// NavigationHistory belongs to one workspace and is used on its UI thread.
type NavigationHistory struct {
	entries []NavigationEntry
	index   int
}

const navigationHistoryLimit = 100

// ReplaceCurrent captures page-local changes, such as a queue filter, before
// leaving the page. It also establishes the initial page on first navigation.
func (h *NavigationHistory) ReplaceCurrent(entry NavigationEntry) {
	if len(h.entries) == 0 {
		h.entries = []NavigationEntry{entry}
		h.index = 0
		return
	}
	h.entries[h.index] = entry
}

func (h *NavigationHistory) Visit(entry NavigationEntry) {
	if len(h.entries) == 0 {
		h.ReplaceCurrent(entry)
		return
	}
	if h.entries[h.index] == entry {
		return
	}
	h.entries = append(h.entries[:h.index+1], entry)
	if len(h.entries) > navigationHistoryLimit {
		copy(h.entries, h.entries[len(h.entries)-navigationHistoryLimit:])
		h.entries = h.entries[:navigationHistoryLimit]
	}
	h.index = len(h.entries) - 1
}

func (h *NavigationHistory) target(direction int, available func(NavigationEntry) bool) (int, bool) {
	if direction != -1 && direction != 1 {
		return 0, false
	}
	for index := h.index + direction; index >= 0 && index < len(h.entries); index += direction {
		if available == nil || available(h.entries[index]) {
			return index, true
		}
	}
	return 0, false
}

func (h *NavigationHistory) CanTravel(direction int, available func(NavigationEntry) bool) bool {
	_, ok := h.target(direction, available)
	return ok
}

func (h *NavigationHistory) Travel(direction int, available func(NavigationEntry) bool) (NavigationEntry, bool) {
	index, ok := h.target(direction, available)
	if !ok {
		return NavigationEntry{}, false
	}
	h.index = index
	return h.entries[index], true
}
