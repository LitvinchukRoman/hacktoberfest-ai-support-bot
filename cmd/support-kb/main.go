// Command support-kb builds the support bot's guide index from the site's articles:
//
//	go run ./cmd/support-kb ../frontend/content
//
// It writes internal/support/kb/guides.json, which is committed and embedded.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/handziurdmytro/skelar/internal/support/kb"
)

func main() {
	out := flag.String("out", "internal/support/kb/guides.json", "where to write the index")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: support-kb [-out file] <frontend/content>")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), *out); err != nil {
		fmt.Fprintln(os.Stderr, "support-kb:", err)
		os.Exit(1)
	}
}

func run(dir, out string) error {
	// Reads stay inside the content directory, even through symlinks.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	content := root.FS()
	var guides []kb.Guide
	err = fs.WalkDir(content, ".", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(file) != ".mdx" || d.Name() == "index.mdx" {
			return err
		}
		src, err := fs.ReadFile(content, file)
		if err != nil {
			return err
		}
		g, err := kb.ParseGuide(path.Base(path.Dir(file)), strings.TrimSuffix(d.Name(), ".mdx"), src)
		if err != nil {
			return fmt.Errorf("%s: %w", file, err)
		}
		guides = append(guides, g)
		return nil
	})
	if err != nil {
		return err
	}
	if len(guides) == 0 {
		return fmt.Errorf("no guides under %s", dir)
	}
	sort.Slice(guides, func(i, j int) bool { return guides[i].Path < guides[j].Path })
	data, err := json.MarshalIndent(guides, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil { // #nosec G306 -- a committed source file
		return err
	}
	fmt.Printf("wrote %d guides to %s\n", len(guides), out)
	return nil
}
