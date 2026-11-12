// Package chm2epub converts Windows CHM help archives into EPUB 3 documents.
//
// The converter is pure Go: the CHM container (ITSF/ITSP/PMGL) and the LZX
// segments are decoded here, .hhc topic trees become the EPUB navigation, and
// the pages are normalised to UTF-8 XHTML. It was imported from the standalone
// chm2epub tool so the desktop build needs neither Calibre nor any subprocess.
package chm2epub

// Version is the converter version, reported in parsed-document metadata.
const Version = "0.1.0"
