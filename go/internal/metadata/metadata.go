package metadata

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/1c-debug-mcp/go/internal/logger"
)

// mdFolders maps configuration folder names to module type prefixes.
var mdFolders = map[string]string{
	"CommonModules":               "CommonModule",
	"Documents":                   "Document",
	"Catalogs":                    "Catalog",
	"DataProcessors":              "DataProcessor",
	"Reports":                     "Report",
	"InformationRegisters":        "InformationRegister",
	"AccumulationRegisters":       "AccumulationRegister",
	"AccountingRegisters":         "AccountingRegister",
	"BusinessProcesses":           "BusinessProcess",
	"Tasks":                       "Task",
	"ExchangePlans":               "ExchangePlan",
	"ChartsOfAccounts":            "ChartOfAccounts",
	"ChartsOfCalculationTypes":    "ChartOfCalculationTypes",
	"ChartsOfCharacteristicTypes": "ChartOfCharacteristicTypes",
	"Constants":                   "Constant",
	"Sequences":                   "Sequence",
	"ScheduledJobs":               "ScheduledJob",
}

// Provider maps objectID (UUID) to human-readable module names.
type Provider struct {
	mu             sync.RWMutex
	objectIDToName map[string]string
	objectIDToExt  map[string]string
	ready          bool
	cfPath         string
	cfePaths       []string
	epfPaths       []string
	cachePath      string
	disableCache   bool
}

// New creates a new Provider.
func New() *Provider {
	return &Provider{
		objectIDToName: make(map[string]string),
		objectIDToExt:  make(map[string]string),
	}
}

// Load starts background loading of metadata from the given paths.
// cachePath overrides the location of the metadata cache file; empty means
// the default (next to the configuration sources).
func (p *Provider) Load(cfPath string, cfePaths, epfPaths []string, cachePath string, disableCache bool) {
	p.mu.Lock()
	p.cfPath = cfPath
	p.cfePaths = cfePaths
	p.epfPaths = epfPaths
	p.cachePath = cachePath
	p.disableCache = disableCache
	p.mu.Unlock()

	go func() {
		skipCache := disableCache
		if err := p.doLoad(cfPath, cfePaths, epfPaths, skipCache); err != nil {
			logger.Error("metadata: load error: %v", err)
		}
		p.mu.Lock()
		p.ready = true
		p.mu.Unlock()
		logger.Info("metadata: loaded and ready (%d modules)", p.ModuleCount())
	}()
}

// Reload clears and reloads all metadata from stored paths.
// If skipCache is true, forces full rescan even if cache is valid.
func (p *Provider) Reload(skipCache bool) (int, error) {
	p.mu.Lock()
	cfPath := p.cfPath
	cfePaths := p.cfePaths
	epfPaths := p.epfPaths
	p.objectIDToName = make(map[string]string)
	p.objectIDToExt = make(map[string]string)
	p.ready = false
	p.mu.Unlock()

	if err := p.doLoad(cfPath, cfePaths, epfPaths, skipCache); err != nil {
		return 0, err
	}

	p.mu.Lock()
	p.ready = true
	count := len(p.objectIDToName)
	p.mu.Unlock()
	return count, nil
}

// IsReady returns true if metadata has been fully loaded.
func (p *Provider) IsReady() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ready
}

// ModuleCount returns the number of loaded modules.
func (p *Provider) ModuleCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.objectIDToName)
}

// ResolveName returns the human-readable module name for a given objectID.
func (p *Provider) ResolveName(objectID string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	name, ok := p.objectIDToName[strings.ToLower(objectID)]
	return name, ok
}

// ResolveExtension returns the extension name for a given objectID.
func (p *Provider) ResolveExtension(objectID string) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.objectIDToExt[strings.ToLower(objectID)]
}

// ResolveObjectID finds the objectID for a given module name.
// Accepts full label like "CommonModule.ОбщегоНазначения" or short name "ОбщегоНазначения".
// If extensionName is empty — searches only in the main configuration (no ":" in label).
// If extensionName is non-empty — searches only in that specific extension.
func (p *Provider) ResolveObjectID(moduleName, extensionName string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	lower := strings.ToLower(moduleName)
	for uuid, label := range p.objectIDToName {
		if extensionName == "" {
			// Main configuration only — skip extension entries
			if strings.Contains(label, ":") {
				continue
			}
		} else {
			// Specific extension only — must start with "ExtName:"
			prefix := strings.ToLower(extensionName) + ":"
			if !strings.HasPrefix(strings.ToLower(label), prefix) {
				continue
			}
		}
		if strings.ToLower(label) == lower {
			return uuid, true
		}
		// match by short name after last dot
		parts := strings.Split(label, ".")
		if strings.ToLower(parts[len(parts)-1]) == lower {
			return uuid, true
		}
	}
	return "", false
}

// doLoad performs the actual loading synchronously.
// If skipCache is true, bypasses cache and forces full rescan.
func (p *Provider) doLoad(cfPath string, cfePaths, epfPaths []string, skipCache bool) error {
	// Check if cache is globally disabled
	p.mu.RLock()
	cacheDisabled := p.disableCache
	p.mu.RUnlock()

	// Try to load from cache first (unless skipCache is true or cache is disabled)
	if !skipCache && !cacheDisabled && p.loadFromCache() {
		return nil
	}

	// Cache miss, invalid, bypassed, or disabled — perform full scan
	if skipCache {
		logger.Info("metadata: cache bypassed, performing full scan")
	} else if cacheDisabled {
		logger.Info("metadata: cache disabled, performing full scan")
	} else {
		logger.Info("metadata: cache miss, performing full scan")
	}

	if cfPath != "" {
		if _, err := os.Stat(cfPath); err == nil {
			before := p.ModuleCount()
			if err := p.scanCF(cfPath, ""); err != nil {
				logger.Error("metadata: error scanning cf: %v", err)
			}
			logger.Info("metadata: loaded %d modules from %s", p.ModuleCount()-before, cfPath)
		} else {
			logger.Error("metadata: cfPath not found: %s", cfPath)
		}
	}

	for _, cfePath := range cfePaths {
		if cfePath == "" {
			continue
		}
		if _, err := os.Stat(cfePath); err != nil {
			logger.Error("metadata: cfePath not found: %s", cfePath)
			continue
		}
		// Check if it's a single extension or a directory of extensions
		configXML := filepath.Join(cfePath, "Configuration.xml")
		if _, err := os.Stat(configXML); err == nil {
			// Single extension directory
			extName := extractExtensionName(cfePath)
			if extName == "" {
				extName = filepath.Base(cfePath)
			}
			before := p.ModuleCount()
			if err := p.scanCF(cfePath, extName); err != nil {
				logger.Error("metadata: error scanning extension %s: %v", extName, err)
			}
			logger.Info("metadata: loaded %d modules from extension %s (%s)", p.ModuleCount()-before, extName, cfePath)
		} else {
			// Directory containing multiple extensions
			entries, err := os.ReadDir(cfePath)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				extDir := filepath.Join(cfePath, entry.Name())
				if _, err := os.Stat(filepath.Join(extDir, "Configuration.xml")); err != nil {
					continue
				}
				extName := extractExtensionName(extDir)
				if extName == "" {
					extName = entry.Name()
				}
				before := p.ModuleCount()
				if err := p.scanCF(extDir, extName); err != nil {
					logger.Error("metadata: error scanning extension %s: %v", extName, err)
				}
				logger.Info("metadata: loaded %d modules from extension %s (%s)", p.ModuleCount()-before, extName, extDir)
			}
		}
	}

	for _, epfPath := range epfPaths {
		if epfPath == "" {
			continue
		}
		if _, err := os.Stat(epfPath); err != nil {
			logger.Error("metadata: epfPath not found: %s", epfPath)
			continue
		}
		before := p.ModuleCount()
		if err := p.scanEPF(epfPath); err != nil {
			logger.Error("metadata: error scanning epf: %v", err)
		}
		logger.Info("metadata: loaded %d modules from EPF path %s", p.ModuleCount()-before, epfPath)
	}

	// Save to cache after successful scan (only if cache is not disabled)
	if !cacheDisabled {
		if err := p.saveToCache(); err != nil {
			logger.Error("metadata: failed to save cache: %v", err)
		}
	}

	return nil
}

// scanCF scans a configuration directory for module XML files.
// Uses parallel processing for better performance.
func (p *Provider) scanCF(cfPath, extensionName string) error {
	type scanResult struct {
		uuid  string
		label string
		ext   string
	}

	results := make(chan scanResult, 100)
	var wg sync.WaitGroup

	// Worker function to process XML files
	processXML := func(xmlPath, name, typePrefix, extensionName string) {
		defer wg.Done()
		uuid := extractUUID(xmlPath)
		if uuid != "" {
			var label string
			if extensionName != "" {
				label = fmt.Sprintf("%s:%s.%s", extensionName, typePrefix, name)
			} else {
				label = fmt.Sprintf("%s.%s", typePrefix, name)
			}
			results <- scanResult{uuid: strings.ToLower(uuid), label: label, ext: extensionName}
		}
	}

	// Start collector goroutine
	done := make(chan struct{})
	go func() {
		for res := range results {
			p.mu.Lock()
			p.objectIDToName[res.uuid] = res.label
			p.objectIDToExt[res.uuid] = res.ext
			p.mu.Unlock()
		}
		close(done)
	}()

	for folder, typePrefix := range mdFolders {
		folderPath := filepath.Join(cfPath, folder)
		if _, err := os.Stat(folderPath); err != nil {
			continue
		}
		entries, err := os.ReadDir(folderPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".xml") {
				continue
			}
			xmlPath := filepath.Join(folderPath, entry.Name())
			name := strings.TrimSuffix(entry.Name(), ".xml")

			wg.Add(1)
			go processXML(xmlPath, name, typePrefix, extensionName)

			// Scan forms
			formsPath := filepath.Join(folderPath, name, "Forms")
			if _, err := os.Stat(formsPath); err != nil {
				continue
			}
			formEntries, err := os.ReadDir(formsPath)
			if err != nil {
				continue
			}
			for _, formEntry := range formEntries {
				if formEntry.IsDir() || !strings.HasSuffix(formEntry.Name(), ".xml") {
					continue
				}
				formXMLPath := filepath.Join(formsPath, formEntry.Name())
				formName := strings.TrimSuffix(formEntry.Name(), ".xml")

				wg.Add(1)
				go func(xmlPath, objName, formName, typePrefix, extensionName string) {
					defer wg.Done()
					formUUID := extractUUID(xmlPath)
					if formUUID != "" {
						var label string
						if extensionName != "" {
							label = fmt.Sprintf("%s:%s.%s/Form/%s", extensionName, typePrefix, objName, formName)
						} else {
							label = fmt.Sprintf("%s.%s/Form/%s", typePrefix, objName, formName)
						}
						results <- scanResult{uuid: strings.ToLower(formUUID), label: label, ext: extensionName}
					}
				}(formXMLPath, name, formName, typePrefix, extensionName)
			}
		}
	}

	wg.Wait()
	close(results)
	<-done

	return nil
}

// scanEPF scans external data processors/reports directory.
func (p *Provider) scanEPF(epfRoot string) error {
	return filepath.WalkDir(epfRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".xml") {
			return nil
		}
		// Only process top-level XML files (ProcessorName/ProcessorName.xml)
		rel, _ := filepath.Rel(epfRoot, path)
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 2 || parts[0]+".xml" != parts[1] {
			return nil
		}
		entryName := parts[0]
		uuid := extractUUID(path)
		if uuid == "" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		typePrefix := "ExternalDataProcessor"
		if strings.Contains(string(content), "<ExternalReport ") || strings.Contains(string(content), "<ExternalReport\n") {
			typePrefix = "ExternalReport"
		}
		label := fmt.Sprintf("%s.%s", typePrefix, entryName)
		p.mu.Lock()
		p.objectIDToName[strings.ToLower(uuid)] = label
		p.objectIDToExt[strings.ToLower(uuid)] = ""
		p.mu.Unlock()
		return nil
	})
}

// uuidRegex matches UUID attributes in XML files.
var uuidRegex = regexp.MustCompile(`uuid="([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"`)

// extractUUID reads the first uuid attribute from an XML file.
// Optimized to read only the first 2KB instead of the entire file.
func extractUUID(xmlPath string) string {
	f, err := os.Open(xmlPath)
	if err != nil {
		return ""
	}
	defer f.Close()

	// Read only first 2KB — UUID is always at the beginning
	buf := make([]byte, 2048)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return ""
	}

	m := uuidRegex.FindSubmatch(buf[:n])
	if m == nil {
		return ""
	}
	return string(m[1])
}

// configNameRegex matches the Name element in Configuration.xml.
var configNameRegex = regexp.MustCompile(`<Name>([^<]+)</Name>`)

// extractExtensionName reads the extension name from Configuration.xml.
func extractExtensionName(cfePath string) string {
	configXML := filepath.Join(cfePath, "Configuration.xml")
	content, err := os.ReadFile(configXML)
	if err != nil {
		return ""
	}
	m := configNameRegex.FindSubmatch(content)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// xmlName is used for parsing Configuration.xml name.
type xmlName struct {
	XMLName xml.Name `xml:"MetaDataObject"`
	Name    string   `xml:"Configuration>Properties>Name"`
}
