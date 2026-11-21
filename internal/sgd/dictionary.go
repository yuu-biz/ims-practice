package sgd

import (
	"bytes"
	_ "embed"
	"sync"

	"github.com/fiorix/go-diameter/v4/diam/dict"
)

var (
	//go:embed sgd.xml
	sgdDictionaryXML []byte
	//go:embed s6c.xml
	s6cDictionaryXML []byte
)

var (
	loadDictionaryOnce sync.Once
	errLoadDictionary  error
)

// LoadDictionary adds the SGd and S6c applications to go-diameter's
// default dictionary. A process must call it before it builds or reads a
// message of this package. Loading the same XML twice would duplicate
// it, so later calls return the result of the first.
func LoadDictionary() error {
	loadDictionaryOnce.Do(func() {
		for _, doc := range [][]byte{sgdDictionaryXML, s6cDictionaryXML} {
			if errLoadDictionary = dict.Default.Load(bytes.NewReader(doc)); errLoadDictionary != nil {
				return
			}
		}
	})
	return errLoadDictionary
}
