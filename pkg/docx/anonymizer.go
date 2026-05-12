package docx

import (
	"github.com/bornholm/go-anon/pkg/anonymizer"
	"github.com/bornholm/go-anon/pkg/docprocessor"
	godocx "github.com/gomutex/godocx"
)

// OpenAndProcess ouvre un fichier DOCX, l'anonymise via proc, sauvegarde le résultat
// dans outputPath, et retourne la session (mapping complet du document).
func OpenAndProcess(inputPath, outputPath string, proc *docprocessor.Processor, opts ...anonymizer.AnonymizeOption) (*anonymizer.Session, error) {
	rd, err := godocx.OpenDocument(inputPath)
	if err != nil {
		return nil, err
	}

	walker := NewWalker(rd)
	session, err := proc.Process(walker, opts...)
	if err != nil {
		return nil, err
	}

	if err := walker.SaveTo(outputPath); err != nil {
		return nil, err
	}
	return session, nil
}
