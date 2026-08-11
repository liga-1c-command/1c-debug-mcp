package metadata

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/1c-debug-mcp/go/internal/logger"
)

// CacheEntry represents a single module in the cache.
type CacheEntry struct {
	Name string `json:"name"`
	Ext  string `json:"ext"`
}

// MetadataCache represents the cached metadata structure.
type MetadataCache struct {
	Version   string                 `json:"version"`
	Timestamp time.Time              `json:"timestamp"`
	PathsHash string                 `json:"pathsHash"`
	Modules   map[string]CacheEntry  `json:"modules"`
}

const cacheVersion = "1.0"

// cacheFileName is the default name of the cache file.
const cacheFileName = ".1c-debug-metadata-cache.json"

// getCachePath returns the path to the cache file.
// An explicit path (ONEC_CACHE_PATH) wins; if it points to an existing
// directory, the default file name is appended. Without it the cache is
// stored in the same directory as the configuration.
func (p *Provider) getCachePath() string {
	if p.cachePath != "" {
		if info, err := os.Stat(p.cachePath); err == nil && info.IsDir() {
			return filepath.Join(p.cachePath, cacheFileName)
		}
		return p.cachePath
	}
	if p.cfPath == "" {
		return ""
	}
	dir := filepath.Dir(p.cfPath)
	return filepath.Join(dir, cacheFileName)
}

// computePathsHash creates a hash of all paths to detect configuration changes.
func (p *Provider) computePathsHash() string {
	h := sha256.New()
	h.Write([]byte(p.cfPath))
	for _, path := range p.cfePaths {
		h.Write([]byte(path))
	}
	for _, path := range p.epfPaths {
		h.Write([]byte(path))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// isCacheValid checks if the cache file exists and is valid.
func (p *Provider) isCacheValid(cachePath string) bool {
	if cachePath == "" {
		return false
	}

	info, err := os.Stat(cachePath)
	if err != nil {
		return false
	}

	cacheTime := info.ModTime()

	// Check if Configuration.xml was modified after cache
	if p.cfPath != "" {
		configXML := filepath.Join(p.cfPath, "Configuration.xml")
		if cfInfo, err := os.Stat(configXML); err == nil {
			if cfInfo.ModTime().After(cacheTime) {
				logger.Info("metadata: cache invalidated (Configuration.xml modified)")
				return false
			}
		}

		// Check if any metadata folder was modified after cache
		if p.hasNewerFiles(p.cfPath, cacheTime) {
			logger.Info("metadata: cache invalidated (source files modified)")
			return false
		}
	}

	// Check extensions
	for _, cfePath := range p.cfePaths {
		if cfePath == "" {
			continue
		}
		if p.hasNewerFiles(cfePath, cacheTime) {
			logger.Info("metadata: cache invalidated (extension files modified)")
			return false
		}
	}

	// Check EPF paths
	for _, epfPath := range p.epfPaths {
		if epfPath == "" {
			continue
		}
		if p.hasNewerFiles(epfPath, cacheTime) {
			logger.Info("metadata: cache invalidated (EPF files modified)")
			return false
		}
	}

	return true
}

// hasNewerFiles checks if any file in metadata folders is newer than cacheTime.
// Only checks top-level folders to avoid deep recursion.
func (p *Provider) hasNewerFiles(rootPath string, cacheTime time.Time) bool {
	// Check known metadata folders
	for folder := range mdFolders {
		folderPath := filepath.Join(rootPath, folder)
		if info, err := os.Stat(folderPath); err == nil {
			if info.ModTime().After(cacheTime) {
				return true
			}
		}
	}
	return false
}

// loadFromCache attempts to load metadata from cache.
func (p *Provider) loadFromCache() bool {
	cachePath := p.getCachePath()
	if !p.isCacheValid(cachePath) {
		return false
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		return false
	}

	var cache MetadataCache
	if err := json.Unmarshal(data, &cache); err != nil {
		logger.Error("metadata: cache parse error: %v", err)
		return false
	}

	if cache.Version != cacheVersion {
		logger.Info("metadata: cache version mismatch (expected %s, got %s)", cacheVersion, cache.Version)
		return false
	}

	currentHash := p.computePathsHash()
	if cache.PathsHash != currentHash {
		logger.Info("metadata: cache invalidated (paths changed)")
		return false
	}

	p.mu.Lock()
	p.objectIDToName = make(map[string]string, len(cache.Modules))
	p.objectIDToExt = make(map[string]string, len(cache.Modules))
	for uuid, entry := range cache.Modules {
		p.objectIDToName[uuid] = entry.Name
		p.objectIDToExt[uuid] = entry.Ext
	}
	p.mu.Unlock()

	logger.Info("metadata: loaded %d modules from cache (%s)", len(cache.Modules), cachePath)
	return true
}

// saveToCache saves the current metadata to cache.
func (p *Provider) saveToCache() error {
	cachePath := p.getCachePath()
	if cachePath == "" {
		return nil
	}

	p.mu.RLock()
	modules := make(map[string]CacheEntry, len(p.objectIDToName))
	for uuid, name := range p.objectIDToName {
		modules[uuid] = CacheEntry{
			Name: name,
			Ext:  p.objectIDToExt[uuid],
		}
	}
	p.mu.RUnlock()

	cache := MetadataCache{
		Version:   cacheVersion,
		Timestamp: time.Now(),
		PathsHash: p.computePathsHash(),
		Modules:   modules,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	if dir := filepath.Dir(cachePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("create cache dir: %w", err)
		}
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		return fmt.Errorf("write cache: %w", err)
	}

	logger.Info("metadata: saved %d modules to cache (%s)", len(modules), cachePath)
	return nil
}
