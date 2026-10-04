// Genicon renders resources/icon.svg to resources/icon.png, the app icon,
// with MyGo's own renderer.
//
//	go run ./tools/genicon
package main

import (
	"image/png"
	"log"
	"os"

	"github.com/egoist/mygo/ui"
)

func main() {
	data, err := os.ReadFile("resources/icon.svg")
	if err != nil {
		log.Fatal(err)
	}
	svg := ui.MustParseSVG(data)
	img := ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Image(c, svg).Size(1024, 1024)
	}, 1024, 1024, 1)
	f, err := os.Create("resources/icon.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
}
