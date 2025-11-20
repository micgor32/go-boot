// Copyright (c) The go-boot authors. All Rights Reserved.
//
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

// Package uapi implements Boot Loader Entries parsing
// following the specifications at:
//
//	https://uapi-group.org/specifications/specs/boot_loader_specification
package uapi

import (
	"bufio"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

// Entry represents the contents loaded from a Type #1 Boot Loader Entry.
type Entry struct {
	// Title is the human-readable entry title.
	Title string
	// Linux is the kernel image to execute.
	Linux []byte
	// Initrd is the ramdisk cpio image, multiple entries are concatenated.
	Initrd []byte
	// Options is the kernel parameters.
	Options string

	parsed  string
	ignored string

	fsys fs.FS
}

func (e *Entry) loadKeyValue(v string) ([]byte, error) {
	v = strings.ReplaceAll(v, `/`, `\`)
	return fs.ReadFile(e.fsys, v)
}

func (e *Entry) parseKey(line string) (err error) {
	kv := strings.SplitN(line, " ", 2)

	if len(kv) < 2 {
		return
	}

	k := kv[0]
	v := strings.Trim(kv[1], "\n\r")
	v = strings.TrimSpace(v)

	switch k {
	case "title":
		e.Title = v
	case "linux":
		if e.Linux, err = e.loadKeyValue(v); err != nil {
			return
		}
	case "initrd":
		var initrd []byte

		if initrd, err = e.loadKeyValue(v); err != nil {
			return
		}

		e.Initrd = append(e.Initrd, initrd...)
	case "options":
		e.Options += v
	default:
		e.ignored += line
		return
	}

	e.parsed += line

	return
}

// String returns the successfully parsed entry keys.
func (e *Entry) String() string {
	return e.parsed
}

// Ignored returns the entry keys ignored during parsing.
func (e *Entry) Ignored() string {
	return e.ignored
}

// LoadEntry parses Type #1 Boot Loader Specification Entries from the argument
// file and loads each key contents from the argument file system.
func LoadEntry(fsys fs.FS, path string) (e *Entry, err error) {
	e = &Entry{
		fsys: fsys,
	}

	entry, err := fs.ReadFile(fsys, path)

	if err != nil {
		return nil, fmt.Errorf("error reading entry file, %v", err)
	}

	for line := range strings.Lines(string(entry)) {
		if err = e.parseKey(line); err != nil {
			return nil, fmt.Errorf("error parsing entry line, %v line:%s", err, line)
		}
	}

	return
}

func ExtractGrubMenuentries(data string) ([]string, error) {
	var entries []string
	lines := strings.Split(data, "\n")

	var collecting bool
	var braceLevel int
	var current []string

	menuentryStart := regexp.MustCompile(`^\s*menuentry\s+'([^']+)'`)

	for _, line := range lines {
		if !collecting {
			if menuentryStart.MatchString(line) {
				collecting = true
				braceLevel = 0
				current = []string{line}

				if strings.Contains(line, "{") {
					braceLevel++
					if strings.Count(line, "}") > 0 {
						braceLevel -= strings.Count(line, "}")
					}
				}
			}
			continue
		}

		current = append(current, line)

		braceLevel += strings.Count(line, "{")
		braceLevel -= strings.Count(line, "}")

		if braceLevel == 0 {
			entries = append(entries, strings.Join(current, "\n"))
			collecting = false
			current = nil
		}
	}

	return entries, nil
}

func LoadGrubEntry(fsys fs.FS, path string) (e *Entry, err error) {
	e = &Entry{
		fsys: fsys,
	}

	toParse, err := fs.ReadFile(fsys, path)
	blocks, _ := ExtractGrubMenuentries(string(toParse))
	// There could be many more entries, but in similar
	// fashion to how it is done with standard UAPI boot
	// entry, lets take just the first entry
	menuentryBlock := blocks[0]

	titleRe := regexp.MustCompile(`menuentry\s+'([^']+)'`)
	m := titleRe.FindStringSubmatch(menuentryBlock)
	if m == nil {
		return nil, fmt.Errorf("could not parse menuentry title")
	}
	e.Title = m[1]

	bodyRe := regexp.MustCompile(`\{([^}]*)\}`)
	m = bodyRe.FindStringSubmatch(menuentryBlock)
	if m == nil {
		return nil, fmt.Errorf("menuentry block missing braces")
	}
	body := m[1]

	sc := bufio.NewScanner(strings.NewReader(body))

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		switch fields[0] {
		case "linux":
			if len(fields) < 2 {
				continue
			}
			kernel := fields[1]

			data, err := e.loadKeyValue(kernel)
			if err != nil {
				return nil, fmt.Errorf("loading linux image %s: %w", kernel, err)
			}
			e.Linux = data

			if len(fields) > 2 {
				e.Options = strings.Join(fields[2:], " ")
			}

		case "initrd":
			if len(fields) < 2 {
				continue
			}
			for _, p := range fields[1:] {
				initrd, err := e.loadKeyValue(p)
				if err != nil {
					return nil, fmt.Errorf("loading initrd %s: %w", p, err)
				}
				e.Initrd = append(e.Initrd, initrd...)
			}

		default:
			e.ignored += line + "\n"
		}

		e.parsed += line + "\n"
	}

	if err := sc.Err(); err != nil {
		return nil, err
	}

	return
}
