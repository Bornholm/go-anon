// Commande brown-cluster — génère des Brown clusters depuis un corpus texte.
//
// Implémentation de l'algorithme de Percy Liang (2005) :
// - table de coûts précomputée O(C²) avec mise à jour incrémentale
// - deltaMI O(|active|) par calcul
// - complexité : O(C³ + V·C²)
//
// Paramètres recommandés pour NER :
//   -clusters 1000 -vocab 20000 -min-count 5
//
// Usage :
//
//	brown-cluster -input corpus.wikiner -format wikiner -output clusters_fr.txt
//
// Format de sortie (compatible wcluster / BrownClusters loader) :
//
//	<chemin_binaire>\t<mot>\t<count>
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
)

func main() {
	inputPath := flag.String("input", "", "corpus d'entrée (obligatoire)")
	outputPath := flag.String("output", "brown_clusters_fr.txt", "fichier de sortie")
	numClusters := flag.Int("clusters", 1000, "nombre cible de clusters Brown")
	maxVocab := flag.Int("vocab", 20000, "taille max du vocabulaire")
	format := flag.String("format", "text", `format : "text" (espaces) ou "wikiner" (FORME|POS|NER)`)
	minCount := flag.Int("min-count", 5, "seuil de fréquence minimum")

	flag.Parse()

	if *inputPath == "" {
		fmt.Fprintln(os.Stderr, "erreur : -input est obligatoire")
		flag.Usage()
		os.Exit(1)
	}

	log.Printf("lecture corpus : %s (format=%s)", *inputPath, *format)
	f, err := os.Open(*inputPath)
	if err != nil {
		log.Fatalf("ouverture : %v", err)
	}
	uf, bf := readNgrams(f, *format)
	f.Close()
	log.Printf("vocabulaire brut : %d mots, %d bigrammes uniques", len(uf), len(bf))

	vocab := topVocab(uf, *maxVocab, *minCount)
	log.Printf("vocabulaire retenu : %d mots (min-count=%d)", len(vocab), *minCount)

	paths := brownCluster(vocab, uf, bf, *numClusters)

	out, err := os.Create(*outputPath)
	if err != nil {
		log.Fatalf("création sortie : %v", err)
	}
	defer out.Close()
	bw := bufio.NewWriter(out)
	for _, w := range vocab {
		fmt.Fprintf(bw, "%s\t%s\t%d\n", paths[w], w, uf[w])
	}
	bw.Flush()
	log.Printf("Brown clusters écrits : %s (%d entrées)", *outputPath, len(vocab))
}

// ---- Lecture du corpus ----

func readNgrams(f *os.File, format string) (map[string]int, map[[2]string]int) {
	uf := make(map[string]int)
	bf := make(map[[2]string]int)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 10<<20), 10<<20)
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "#") {
			continue
		}
		var toks []string
		if format == "wikiner" {
			for _, raw := range strings.Fields(line) {
				if p := strings.SplitN(raw, "|", 3); len(p) >= 1 && p[0] != "" {
					toks = append(toks, strings.ToLower(p[0]))
				}
			}
		} else {
			for _, t := range strings.Fields(line) {
				toks = append(toks, strings.ToLower(t))
			}
		}
		for _, t := range toks {
			uf[t]++
		}
		for i := 1; i < len(toks); i++ {
			bf[[2]string{toks[i-1], toks[i]}]++
		}
		n++
		if n%100000 == 0 {
			log.Printf("  %d phrases lues, vocab=%d", n, len(uf))
		}
	}
	return uf, bf
}

func topVocab(uf map[string]int, maxV, minC int) []string {
	type wc struct {
		w string
		c int
	}
	var list []wc
	for w, c := range uf {
		if c >= minC {
			list = append(list, wc{w, c})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].c != list[j].c {
			return list[i].c > list[j].c
		}
		return list[i].w < list[j].w
	})
	if len(list) > maxV {
		list = list[:maxV]
	}
	out := make([]string, len(list))
	for i, x := range list {
		out[i] = x.w
	}
	return out
}

// ---- Algorithme de Brown Clustering ----

type cid = int32
type bigram = [2]cid

func brownCluster(vocab []string, uf map[string]int, bf map[[2]string]int, C int) map[string]string {
	V := len(vocab)
	if C > V {
		C = V
	}

	// Index des mots
	wordIdx := make(map[string]cid, V)
	for i, w := range vocab {
		wordIdx[w] = cid(i)
	}

	// Comptes unigrammes et total tokens
	maxNodes := 2*V + 16
	cf1 := make([]int64, maxNodes)
	for i, w := range vocab {
		cf1[i] = int64(uf[w])
	}
	var N int64
	for i := range V {
		N += cf1[i]
	}
	Nf := float64(N)

	// Bigrammes dans l'espace des IDs de mots
	cf2 := make(map[bigram]int64, len(bf))
	for k, c := range bf {
		ia, oka := wordIdx[k[0]]
		ib, okb := wordIdx[k[1]]
		if oka && okb {
			cf2[bigram{ia, ib}] += int64(c)
		}
	}

	// Voisins bigrammes par mot (listes précomputées pour la phase 1)
	type nb struct{ w cid; c int64 }
	fwdW := make([][]nb, V) // fwdW[w] = liste des mots suivant w avec leur count
	bwdW := make([][]nb, V) // bwdW[w] = liste des mots précédant w avec leur count
	for k, c := range cf2 {
		if int(k[0]) < V && int(k[1]) < V {
			fwdW[k[0]] = append(fwdW[k[0]], nb{k[1], c})
			bwdW[k[1]] = append(bwdW[k[1]], nb{k[0], c})
		}
	}

	// Arbre de fusion
	mergeLeft := make([]cid, maxNodes)
	mergeRight := make([]cid, maxNodes)
	for i := range mergeLeft {
		mergeLeft[i] = -1
		mergeRight[i] = -1
	}

	// Cluster courant de chaque mot (pour la phase 1)
	wc := make([]cid, V)
	for i := range wc {
		wc[i] = cid(i)
	}

	// Liste des membres de chaque cluster (pour mise à jour de wc après fusion)
	// On maintient une liste chaînée via un tableau.
	// clusterHead[c] = premier mot membre (dans la liste liée via nextMember)
	// nextMember[i] = prochain mot dans le cluster de i, ou -1 si fin
	clusterHead := make([]cid, maxNodes)
	nextMember := make([]cid, maxNodes)
	for i := range clusterHead {
		clusterHead[i] = -1
	}
	for i := range nextMember {
		nextMember[i] = -1
	}
	for i := range V {
		clusterHead[i] = cid(i)
		nextMember[i] = -1
	}

	nextID := cid(V)

	// Clusters actifs
	active := make([]cid, 0, C+2)
	for i := 0; i < C && i < V; i++ {
		active = append(active, cid(i))
	}

	h2 := func(a, b cid) float64 {
		c2 := cf2[bigram{a, b}]
		if c2 == 0 || cf1[a] == 0 || cf1[b] == 0 {
			return 0
		}
		return float64(c2) / Nf * math.Log(float64(c2)*Nf/float64(cf1[a]*cf1[b]))
	}

	deltaMI := func(l, r cid) float64 {
		flr := cf1[l] + cf1[r]
		if flr == 0 {
			return math.Inf(-1)
		}
		var delta float64
		flrF := float64(flr)
		for _, c := range active {
			if c == l || c == r {
				continue
			}
			fc := float64(cf1[c])
			f_lrc := cf2[bigram{l, c}] + cf2[bigram{r, c}]
			f_clr := cf2[bigram{c, l}] + cf2[bigram{c, r}]
			if f_lrc > 0 {
				delta += float64(f_lrc) / Nf * math.Log(float64(f_lrc)*Nf/(flrF*fc))
			}
			if f_clr > 0 {
				delta += float64(f_clr) / Nf * math.Log(float64(f_clr)*Nf/(fc*flrF))
			}
			delta -= h2(l, c) + h2(r, c) + h2(c, l) + h2(c, r)
		}
		f_lr := cf2[bigram{l, l}] + cf2[bigram{r, r}] + cf2[bigram{l, r}] + cf2[bigram{r, l}]
		if f_lr > 0 {
			delta += float64(f_lr) / Nf * math.Log(float64(f_lr)*Nf/(flrF*flrF))
		}
		delta -= h2(l, l) + h2(r, r) + h2(l, r) + h2(r, l)
		return delta
	}

	// Table de coûts : (l, r) → deltaMI, avec l < r pour l'ordre canonique
	type cpair = [2]cid
	canon := func(a, b cid) cpair {
		if a < b {
			return cpair{a, b}
		}
		return cpair{b, a}
	}
	scores := make(map[cpair]float64, C*(C-1)/2)
	for i := 0; i < len(active); i++ {
		for j := i + 1; j < len(active); j++ {
			scores[canon(active[i], active[j])] = deltaMI(active[i], active[j])
		}
	}
	log.Printf("table de coûts initiale : %d entrées (C=%d)", len(scores), C)

	findBest := func() (cid, cid) {
		best := math.Inf(-1)
		var bl, br cid = -1, -1
		for k, s := range scores {
			if s > best {
				best = s
				bl, br = k[0], k[1]
			}
		}
		return bl, br
	}

	extend := func() {
		if int(nextID) < len(cf1) {
			return
		}
		cf1 = append(cf1, make([]int64, len(cf1))...)
		ext := make([]cid, len(mergeLeft))
		for i := range ext {
			ext[i] = -1
		}
		mergeLeft = append(mergeLeft, ext...)
		mergeRight = append(mergeRight, append([]cid{}, ext...)...)
		clusterHead = append(clusterHead, append([]cid{}, ext...)...)
		nextMember = append(nextMember, append([]cid{}, ext...)...)
	}

	doMerge := func(l, r cid) cid {
		extend()
		newID := nextID
		nextID++
		mergeLeft[newID] = l
		mergeRight[newID] = r
		cf1[newID] = cf1[l] + cf1[r]

		// Agréger les bigrammes du nouveau cluster
		for _, c := range active {
			if c == l || c == r {
				continue
			}
			if v := cf2[bigram{l, c}] + cf2[bigram{r, c}]; v > 0 {
				cf2[bigram{newID, c}] = v
			}
			if v := cf2[bigram{c, l}] + cf2[bigram{c, r}]; v > 0 {
				cf2[bigram{c, newID}] = v
			}
		}
		if v := cf2[bigram{l, l}] + cf2[bigram{r, r}] + cf2[bigram{l, r}] + cf2[bigram{r, l}]; v > 0 {
			cf2[bigram{newID, newID}] = v
		}

		// Supprimer les anciens bigrammes
		for _, c := range active {
			delete(cf2, bigram{l, c})
			delete(cf2, bigram{r, c})
			delete(cf2, bigram{c, l})
			delete(cf2, bigram{c, r})
		}

		// Supprimer les scores impliquant l ou r
		for k := range scores {
			if k[0] == l || k[1] == l || k[0] == r || k[1] == r {
				delete(scores, k)
			}
		}

		// Mettre à jour active
		newActive := active[:0]
		for _, c := range active {
			if c != l && c != r {
				newActive = append(newActive, c)
			}
		}
		newActive = append(newActive, newID)
		active = newActive

		// Calculer les nouveaux scores pour newID
		for _, c := range active {
			if c == newID {
				continue
			}
			scores[canon(newID, c)] = deltaMI(newID, c)
		}

		// Fusionner les listes de membres (liste chaînée)
		// Trouver la queue de la liste de l, puis y attacher la liste de r
		if clusterHead[l] >= 0 {
			// Trouver la fin de la liste de l
			tail := clusterHead[l]
			for nextMember[tail] >= 0 {
				tail = nextMember[tail]
			}
			nextMember[tail] = clusterHead[r]
			clusterHead[newID] = clusterHead[l]
		} else {
			clusterHead[newID] = clusterHead[r]
		}
		clusterHead[l] = -1
		clusterHead[r] = -1

		// Mettre à jour wc pour tous les membres du nouveau cluster
		for m := clusterHead[newID]; m >= 0; m = nextMember[m] {
			wc[m] = newID
		}

		return newID
	}

	// ---- Phase 1 : ajout des mots restants un par un ----
	for wordI := C; wordI < V; wordI++ {
		w := cid(wordI)

		// Bigrammes du nouveau mot avec les clusters actifs courants
		for _, nb := range fwdW[w] {
			c := wc[nb.w]
			cf2[bigram{w, c}] += nb.c
		}
		for _, nb := range bwdW[w] {
			c := wc[nb.w]
			cf2[bigram{c, w}] += nb.c
		}

		// Ajouter à active
		active = append(active, w)

		// Calculer les scores entre w et les clusters existants
		for _, c := range active {
			if c == w {
				continue
			}
			scores[canon(w, c)] = deltaMI(w, c)
		}

		// Meilleure fusion
		l, r := findBest()
		if l < 0 || r < 0 || l == r {
			continue
		}
		doMerge(l, r)

		if (wordI-C)%2000 == 0 {
			log.Printf("  phase 1 : %d/%d mots ajoutés, clusters=%d", wordI-C+1, V-C, len(active))
		}
	}

	// ---- Phase 2 : fusion finale ----
	log.Printf("phase 2 : fusion de %d clusters restants", len(active))
	step := 0
	for len(active) > 1 {
		l, r := findBest()
		if l < 0 || r < 0 || l == r {
			break
		}
		doMerge(l, r)
		step++
		if step%100 == 0 {
			log.Printf("  clusters restants : %d", len(active))
		}
	}

	// ---- Reconstruction des chemins via DFS ----
	wordPath := make(map[string]string, V)
	for _, w := range vocab {
		wordPath[w] = ""
	}

	var dfs func(c cid, path string)
	dfs = func(c cid, path string) {
		if int(c) < V {
			wordPath[vocab[c]] = path
			return
		}
		if mergeLeft[c] >= 0 {
			dfs(mergeLeft[c], path+"0")
		}
		if mergeRight[c] >= 0 {
			dfs(mergeRight[c], path+"1")
		}
	}
	if len(active) > 0 {
		dfs(active[0], "")
	}
	for i, w := range vocab {
		if wordPath[w] == "" {
			wordPath[w] = fmt.Sprintf("%020b", i)
		}
	}
	return wordPath
}
