package browserextensionassets

import (
	"embed"
	"io/fs"
	"path"
	"sort"
)

//go:embed chrome/dist
var chromeExtensionFS embed.FS

func ChromeSubFS() (fs.FS, error) {
	return fs.Sub(chromeExtensionFS, "chrome/dist")
}

func ChromeFiles() []string {
	items := []string{}
	_ = fs.WalkDir(chromeExtensionFS, "chrome/dist", func(assetPath string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		items = append(items, path.Clean(assetPath))
		return nil
	})
	sort.Strings(items)
	return items
}
