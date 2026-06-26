package index

import "sync"

// ConfigLoader is a function that returns the current IndexConfig.
// It is called on every reindex so that config changes (e.g. new repos
// added to config.yaml) are picked up without restarting the server.
type ConfigLoader func() (IndexConfig, error)

// IndexHolder holds a swappable Index and a config loader to rebuild it.
// All tools reference the holder so that reindex can atomically swap the index.
type IndexHolder struct {
	mu         sync.RWMutex
	idx        *Index
	cfg        IndexConfig
	loadConfig ConfigLoader
}

// NewHolder creates an IndexHolder with the given index and config.
// If a ConfigLoader is provided, Reindex will call it to get fresh config;
// otherwise it falls back to the config provided at construction time.
func NewHolder(idx *Index, cfg IndexConfig) *IndexHolder {
	return &IndexHolder{idx: idx, cfg: cfg}
}

// SetConfigLoader sets a function that reloads config on each reindex.
func (h *IndexHolder) SetConfigLoader(loader ConfigLoader) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loadConfig = loader
}

// Get returns the current index. Callers should not hold the returned pointer
// across long operations — call Get() each time.
func (h *IndexHolder) Get() *Index {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.idx
}

// Reindex rebuilds the index from disk and atomically swaps it in.
// If a ConfigLoader was set, it re-reads the config first so new repos
// are picked up. Returns the new symbol and package counts.
func (h *IndexHolder) Reindex() (symbols int, packages int, err error) {
	cfg := h.cfg

	if h.loadConfig != nil {
		freshCfg, loadErr := h.loadConfig()
		if loadErr != nil {
			return 0, 0, loadErr
		}
		cfg = freshCfg
	}

	newIdx, err := BuildIndex(cfg)
	if err != nil {
		return 0, 0, err
	}

	h.mu.Lock()
	h.idx = newIdx
	h.cfg = cfg
	h.mu.Unlock()

	return len(newIdx.Symbols), len(newIdx.Packages), nil
}
