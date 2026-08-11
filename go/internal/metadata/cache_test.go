package metadata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheInvalidation(t *testing.T) {
	// Create temporary test directory
	tmpDir := t.TempDir()
	cfPath := filepath.Join(tmpDir, "src")
	
	// Create minimal configuration structure
	if err := os.MkdirAll(filepath.Join(cfPath, "CommonModules"), 0755); err != nil {
		t.Fatal(err)
	}
	
	configXML := filepath.Join(cfPath, "Configuration.xml")
	if err := os.WriteFile(configXML, []byte(`<?xml version="1.0"?><MetaDataObject><Configuration><Name>Test</Name></Configuration></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test module
	moduleXML := filepath.Join(cfPath, "CommonModules", "TestModule.xml")
	if err := os.WriteFile(moduleXML, []byte(`<?xml version="1.0"?><MetaDataObject uuid="12345678-1234-1234-1234-123456789abc"><CommonModule/></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	provider := New()
	provider.cfPath = cfPath
	provider.cfePaths = []string{}
	provider.epfPaths = []string{}

	// First load - should scan and create cache
	if err := provider.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	if provider.ModuleCount() != 1 {
		t.Errorf("Expected 1 module, got %d", provider.ModuleCount())
	}

	cachePath := provider.getCachePath()
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal("Cache file not created")
	}

	// Wait a bit to ensure mtime difference
	time.Sleep(10 * time.Millisecond)

	// Second load - should use cache
	provider2 := New()
	provider2.cfPath = cfPath
	provider2.cfePaths = []string{}
	provider2.epfPaths = []string{}

	if !provider2.loadFromCache() {
		t.Error("Cache should be valid but wasn't loaded")
	}

	if provider2.ModuleCount() != 1 {
		t.Errorf("Expected 1 module from cache, got %d", provider2.ModuleCount())
	}

	// Modify Configuration.xml
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(configXML, []byte(`<?xml version="1.0"?><MetaDataObject><Configuration><Name>TestModified</Name></Configuration></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	// Third load - cache should be invalidated
	provider3 := New()
	provider3.cfPath = cfPath
	provider3.cfePaths = []string{}
	provider3.epfPaths = []string{}

	if provider3.loadFromCache() {
		t.Error("Cache should be invalidated after Configuration.xml modification")
	}

	// Modify folder mtime by adding new module
	time.Sleep(10 * time.Millisecond)
	
	// Recreate cache
	provider4 := New()
	provider4.cfPath = cfPath
	provider4.cfePaths = []string{}
	provider4.epfPaths = []string{}
	if err := provider4.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	time.Sleep(10 * time.Millisecond)

	// Add new module (this updates folder mtime)
	newModuleXML := filepath.Join(cfPath, "CommonModules", "NewModule.xml")
	if err := os.WriteFile(newModuleXML, []byte(`<?xml version="1.0"?><MetaDataObject uuid="87654321-4321-4321-4321-cba987654321"><CommonModule/></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	// Fifth load - cache should be invalidated due to folder mtime change
	provider5 := New()
	provider5.cfPath = cfPath
	provider5.cfePaths = []string{}
	provider5.epfPaths = []string{}

	if provider5.loadFromCache() {
		t.Error("Cache should be invalidated after folder modification")
	}
}

func TestCacheStructure(t *testing.T) {
	tmpDir := t.TempDir()
	cfPath := filepath.Join(tmpDir, "src")
	
	if err := os.MkdirAll(filepath.Join(cfPath, "CommonModules"), 0755); err != nil {
		t.Fatal(err)
	}
	
	configXML := filepath.Join(cfPath, "Configuration.xml")
	if err := os.WriteFile(configXML, []byte(`<?xml version="1.0"?><MetaDataObject><Configuration><Name>Test</Name></Configuration></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	moduleXML := filepath.Join(cfPath, "CommonModules", "TestModule.xml")
	if err := os.WriteFile(moduleXML, []byte(`<?xml version="1.0"?><MetaDataObject uuid="12345678-1234-1234-1234-123456789abc"><CommonModule/></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	provider := New()
	provider.cfPath = cfPath
	provider.cfePaths = []string{}
	provider.epfPaths = []string{}

	if err := provider.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	cachePath := provider.getCachePath()
	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}

	var cache MetadataCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatal(err)
	}

	if cache.Version != cacheVersion {
		t.Errorf("Expected version %s, got %s", cacheVersion, cache.Version)
	}

	if len(cache.Modules) != 1 {
		t.Errorf("Expected 1 module in cache, got %d", len(cache.Modules))
	}

	uuid := "12345678-1234-1234-1234-123456789abc"
	entry, ok := cache.Modules[uuid]
	if !ok {
		t.Error("Module not found in cache")
	}

	if entry.Name != "CommonModule.TestModule" {
		t.Errorf("Expected name 'CommonModule.TestModule', got '%s'", entry.Name)
	}
}

func TestCustomCachePath(t *testing.T) {
	tmpDir := t.TempDir()
	cfPath := filepath.Join(tmpDir, "src")

	if err := os.MkdirAll(filepath.Join(cfPath, "CommonModules"), 0755); err != nil {
		t.Fatal(err)
	}

	configXML := filepath.Join(cfPath, "Configuration.xml")
	if err := os.WriteFile(configXML, []byte(`<?xml version="1.0"?><MetaDataObject><Configuration><Name>Test</Name></Configuration></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	moduleXML := filepath.Join(cfPath, "CommonModules", "TestModule.xml")
	if err := os.WriteFile(moduleXML, []byte(`<?xml version="1.0"?><MetaDataObject uuid="12345678-1234-1234-1234-123456789abc"><CommonModule/></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	// Cache file in a directory that does not exist yet
	customPath := filepath.Join(tmpDir, "local", "tmp", "meta-cache.json")

	provider := New()
	provider.cfPath = cfPath
	provider.cfePaths = []string{}
	provider.epfPaths = []string{}
	provider.cachePath = customPath

	if err := provider.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("Cache file not created at custom path: %v", err)
	}

	// Default location must stay clean
	if _, err := os.Stat(filepath.Join(tmpDir, cacheFileName)); err == nil {
		t.Error("Cache was also written to the default location")
	}

	// Cache from the custom path is picked up on the next start
	provider2 := New()
	provider2.cfPath = cfPath
	provider2.cfePaths = []string{}
	provider2.epfPaths = []string{}
	provider2.cachePath = customPath

	if !provider2.loadFromCache() {
		t.Error("Cache at custom path should be valid but wasn't loaded")
	}

	if provider2.ModuleCount() != 1 {
		t.Errorf("Expected 1 module from cache, got %d", provider2.ModuleCount())
	}

	// A directory as cachePath means "put the default file name in here"
	cacheDir := filepath.Join(tmpDir, "cachedir")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}

	provider3 := New()
	provider3.cfPath = cfPath
	provider3.cachePath = cacheDir

	if got, want := provider3.getCachePath(), filepath.Join(cacheDir, cacheFileName); got != want {
		t.Errorf("Expected cache path %q, got %q", want, got)
	}
}

func TestSkipCache(t *testing.T) {
	tmpDir := t.TempDir()
	cfPath := filepath.Join(tmpDir, "src")
	
	if err := os.MkdirAll(filepath.Join(cfPath, "CommonModules"), 0755); err != nil {
		t.Fatal(err)
	}
	
	configXML := filepath.Join(cfPath, "Configuration.xml")
	if err := os.WriteFile(configXML, []byte(`<?xml version="1.0"?><MetaDataObject><Configuration><Name>Test</Name></Configuration></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	moduleXML := filepath.Join(cfPath, "CommonModules", "TestModule.xml")
	if err := os.WriteFile(moduleXML, []byte(`<?xml version="1.0"?><MetaDataObject uuid="12345678-1234-1234-1234-123456789abc"><CommonModule/></MetaDataObject>`), 0644); err != nil {
		t.Fatal(err)
	}

	provider := New()
	provider.cfPath = cfPath
	provider.cfePaths = []string{}
	provider.epfPaths = []string{}

	// First load - creates cache
	if err := provider.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	cachePath := provider.getCachePath()
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatal("Cache file not created")
	}

	cacheInfo, _ := os.Stat(cachePath)
	time.Sleep(10 * time.Millisecond)

	// Second load with skipCache=false - should use cache
	provider2 := New()
	provider2.cfPath = cfPath
	provider2.cfePaths = []string{}
	provider2.epfPaths = []string{}

	if err := provider2.doLoad(cfPath, nil, nil, false); err != nil {
		t.Fatal(err)
	}

	// Cache file should not be modified (loaded from cache)
	cacheInfo2, _ := os.Stat(cachePath)
	if cacheInfo2.ModTime().After(cacheInfo.ModTime()) {
		t.Error("Cache was regenerated when it should have been loaded")
	}

	time.Sleep(10 * time.Millisecond)

	// Third load with skipCache=true - should bypass cache and rescan
	provider3 := New()
	provider3.cfPath = cfPath
	provider3.cfePaths = []string{}
	provider3.epfPaths = []string{}

	if err := provider3.doLoad(cfPath, nil, nil, true); err != nil {
		t.Fatal(err)
	}

	// Cache file should be regenerated
	cacheInfo3, _ := os.Stat(cachePath)
	if !cacheInfo3.ModTime().After(cacheInfo2.ModTime()) {
		t.Error("Cache was not regenerated when skipCache=true")
	}

	if provider3.ModuleCount() != 1 {
		t.Errorf("Expected 1 module after skipCache, got %d", provider3.ModuleCount())
	}
}
