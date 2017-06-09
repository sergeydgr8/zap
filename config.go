package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Jeffail/gabs"
	"github.com/fsnotify/fsnotify"
	"github.com/ghodss/yaml"
)

const (
	delimStart = "### Zap Shortcuts :start ##\n"
	delimEnd   = "### Zap Shortcuts :end ##\n"
	expandKey  = "expand"
	queryKey   = "query"
	sslKey     = "ssl_off"
)

// parseYaml takes a file name and returns a gabs config object.
func parseYaml(fname string) (*gabs.Container, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		fmt.Printf("Unable to read file: %s\n", err.Error())
		return nil, err
	}
	d, jsonErr := yaml.YAMLToJSON([]byte(data))
	if jsonErr != nil {
		fmt.Printf("Error encoding input to JSON.\n%s\n", jsonErr.Error())
		return nil, jsonErr
	}
	j, _ := gabs.ParseJSON(d)
	return j, nil
}

func watchChanges(watcher *fsnotify.Watcher, fname string, cb func()) {
	for {
		select {
		case event := <-watcher.Events:
			// You may wonder why we can't just listen for "Write" events. The reason is that vim (and other editors)
			// will create swap files, and when you write they delete the original and rename the swap file. This is great
			// for resolving system crashes, but also completely incompatible with inotify and other fswatch implementations.
			// Thus, we check that the file of interest might be created as well.
			updated := event.Op&fsnotify.Create == fsnotify.Create || event.Op&fsnotify.Write == fsnotify.Write
			zapconf := filepath.Clean(event.Name) == fname
			if updated && zapconf {
				cb()
			}
		case e := <-watcher.Errors:
			log.Println("error:", e)
		}
	}
}

// TODO: add tests. simulate touching a file.
// updateHosts will attempt to write the zap list of shortcuts
// to /etc/hosts. It will gracefully fail if there are not enough
// permissions to do so.
func updateHosts(c *context) {
	hostPath := "/etc/hosts"

	// 1. read file, prep buffer.
	data, err := ioutil.ReadFile(hostPath)
	if err != nil {
		log.Println("open config: ", err)
	}
	var replacement bytes.Buffer

	// 2. generate payload.
	replacement.WriteString(delimStart)
	children, _ := c.config.ChildrenMap()
	for k := range children {
		replacement.WriteString(fmt.Sprintf("127.0.0.1 %s\n", k))
	}
	replacement.WriteString(delimEnd)

	// 3. Generate new file content
	var updatedFile string
	if !strings.Contains(string(data), delimStart) {
		updatedFile = string(data) + replacement.String()
	} else {
		zapBlock := regexp.MustCompile("(###(.*)##)\n(.|\n)*(###(.*)##\n)")
		updatedFile = zapBlock.ReplaceAllString(string(data), replacement.String())
	}

	// 4. Attempt write to file.
	err = ioutil.WriteFile(hostPath, []byte(updatedFile), 0644)
	if err != nil {
		log.Printf("Error writing to '%s': %s\n", hostPath, err.Error())
	}
}

// makeCallback returns a func that that updates global state.
func makeCallback(c *context, configName string) func() {
	return func() {
		data, err := parseYaml(configName)
		if err != nil {
			log.Printf("Error in new config: %s. Fallback to old config.", err)
			return
		}

		// Update config atomically
		c.configMtx.Lock()
		c.config = data
		c.configMtx.Unlock()

		// Sync DNS entries.
		updateHosts(c)
		return
	}
}
