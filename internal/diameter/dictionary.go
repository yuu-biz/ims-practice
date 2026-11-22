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

// Dictionary is a set of application dictionaries that go into
// go-diameter's default dictionary. This package keeps the set of Cx
// and Sh, and package sgd the set of SGd and S6c, because a node loads
// only the applications its roles speak.
type Dictionary struct {
	documents [][]byte
	once      sync.Once
	err       error
}

// NewDictionary holds the XML documents that Load then adds.
func NewDictionary(documents ...[]byte) *Dictionary {
	return &Dictionary{documents: documents}
}

// Load adds the documents to go-diameter's default dictionary. Loading
// the same XML twice would duplicate it, so later calls return the
// result of the first.
func (d *Dictionary) Load() error {
	d.once.Do(func() {
		for _, doc := range d.documents {
			if d.err = dict.Default.Load(bytes.NewReader(doc)); d.err != nil {
				return
			}
		}
	})
	return d.err
}

var imsDictionary = NewDictionary(cxDictionaryXML, shDictionaryXML)

// LoadDictionary adds the Cx and Sh applications to go-diameter's
// default dictionary.
func LoadDictionary() error { return imsDictionary.Load() }
