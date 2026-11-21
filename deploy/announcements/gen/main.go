// Command gen writes the WAV file of each announcement of package
// announcements into the current directory. go generate runs it in the
// directory of the package.
package main

import (
	"log"
	"os"

	"github.com/yuu-biz/ims-practice/deploy/announcements"
	"github.com/yuu-biz/ims-practice/internal/wav"
)

func main() {
	for name, samples := range announcements.Clips() {
		f, err := os.Create(name + ".wav")
		if err != nil {
			log.Fatal(err)
		}
		if err := wav.Write(f, samples); err != nil {
			log.Fatal(err)
		}
		if err := f.Close(); err != nil {
			log.Fatal(err)
		}
	}
}
