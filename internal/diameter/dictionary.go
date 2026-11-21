package diameter

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

var (
	//go:embed cx.xml
	cxDictionaryXML []byte
	//go:embed sh.xml
	shDictionaryXML []byte
)

var (
	loadDictionaryOnce sync.Once
	errLoadDictionary  error
)

// LoadDictionary adds the Cx and Sh applications to go-diameter's
// default dictionary. Loading the same XML twice would duplicate it, so
// later calls return the result of the first.
func LoadDictionary() error {
	loadDictionaryOnce.Do(func() {
		for _, doc := range [][]byte{cxDictionaryXML, shDictionaryXML} {
			if errLoadDictionary = dict.Default.Load(bytes.NewReader(doc)); errLoadDictionary != nil {
				return
			}
		}
	})
	return errLoadDictionary
}
