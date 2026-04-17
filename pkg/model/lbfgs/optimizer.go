package lbfgs

import (
	"fmt"
	"math"

	"github.com/bornholm/go-anon/pkg/corpus"
	"github.com/bornholm/go-anon/pkg/features"
	"github.com/bornholm/go-anon/pkg/model"
)

type OptimizerType string

const (
	OptimizerSGD      OptimizerType = "sgd"
	OptimizerMomentum OptimizerType = "momentum"
	OptimizerAdam     OptimizerType = "adam"
	OptimizerLBFGS    OptimizerType = "lbfgs"
)

type OptimizerConfig struct {
	Type         OptimizerType
	MaxIter      int
	LearningRate float64
	L2Lambda     float64
	Momentum     float64
	Beta1        float64
	Beta2        float64
	Epsilon      float64
	Tol          float64
	Verbose      bool
	BatchSize    int
}

func NewOptimizerConfig() OptimizerConfig {
	return OptimizerConfig{
		Type:         OptimizerSGD,
		MaxIter:      100,
		LearningRate: 0.1,
		L2Lambda:     0.01,
		Momentum:     0.9,
		Beta1:        0.9,
		Beta2:        0.999,
		Epsilon:      1e-8,
		Tol:          1e-5,
		Verbose:      false,
		BatchSize:    1,
	}
}

type Optimizer struct {
	config         OptimizerConfig
	iter           int
	mVelEmission   map[uint64]float64
	mVelTransition [][]float64
	vEmission      map[uint64]float64
	vTransition    [][]float64
	bestWeights    map[uint64]float64
	bestTransition [][]float64
}

func New(config OptimizerConfig) *Optimizer {
	if config.MaxIter == 0 {
		config.MaxIter = 100
	}
	if config.LearningRate == 0 {
		config.LearningRate = 0.1
	}
	if config.Epsilon == 0 {
		config.Epsilon = 1e-8
	}

	return &Optimizer{
		config:       config,
		mVelEmission: make(map[uint64]float64),
		vEmission:    make(map[uint64]float64),
	}
}

func (opt *Optimizer) initTransitions(L int) {
	if opt.mVelTransition == nil {
		opt.mVelTransition = make([][]float64, L)
		opt.vTransition = make([][]float64, L)
		opt.bestTransition = make([][]float64, L)
		for i := 0; i < L; i++ {
			opt.mVelTransition[i] = make([]float64, L)
			opt.vTransition[i] = make([]float64, L)
			opt.bestTransition[i] = make([]float64, L)
		}
	}
}

func (opt *Optimizer) saveBest(crf *model.CRF) {
	opt.bestWeights = make(map[uint64]float64, len(crf.Weights.W))
	for k, v := range crf.Weights.W {
		opt.bestWeights[k] = v
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			opt.bestTransition[i][j] = crf.Transition[i][j]
		}
	}
}

func (opt *Optimizer) restoreBest(crf *model.CRF) {
	if opt.bestWeights == nil {
		return
	}
	crf.Weights.W = make(map[uint64]float64, len(opt.bestWeights))
	for k, v := range opt.bestWeights {
		crf.Weights.W[k] = v
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			crf.Transition[i][j] = opt.bestTransition[i][j]
		}
	}
}

type TrainingFunc func(crf *model.CRF, sents []corpus.Sentence, lr float64) (logLikelihood float64, gradEmission map[uint64]float64, gradTransition [][]float64, err error)

func (opt *Optimizer) Optimize(crf *model.CRF, trainSents []corpus.Sentence, extractor *features.FeatureExtractor, trainFn TrainingFunc) (*model.CRF, error) {
	opt.initTransitions(len(crf.Labels))
	opt.iter = 0

	L := len(crf.Labels)
	bestLL := math.Inf(-1)
	opt.saveBest(crf)

	for opt.iter < opt.config.MaxIter {
		batchSize := opt.config.BatchSize
		if batchSize <= 0 || batchSize > len(trainSents) {
			batchSize = len(trainSents)
		}

		var totalLL float64
		var totalGradE map[uint64]float64
		var totalGradT [][]float64

		for i := 0; i < len(trainSents); i += batchSize {
			end := i + batchSize
			if end > len(trainSents) {
				end = len(trainSents)
			}
			batch := trainSents[i:end]

			ll, gradE, gradT, err := trainFn(crf, batch, opt.config.LearningRate)
			if err != nil {
				return nil, fmt.Errorf("training failed: %w", err)
			}

			totalLL += ll

			if totalGradE == nil {
				totalGradE = make(map[uint64]float64)
				totalGradT = make([][]float64, L)
				for j := 0; j < L; j++ {
					totalGradT[j] = make([]float64, L)
				}
			}
			for k, v := range gradE {
				totalGradE[k] += v / float64(len(trainSents))
			}
			for j := 0; j < L; j++ {
				for k := 0; k < L; k++ {
					totalGradT[j][k] += gradT[j][k] / float64(len(trainSents))
				}
			}
		}

		opt.updateWeights(crf, totalGradE, totalGradT)

		llChange := math.Abs(totalLL - bestLL)
		if opt.config.Verbose {
			fmt.Printf("Iter %d: LL=%.4f, ΔLL=%.2e\n", opt.iter, totalLL, llChange)
		}

		if totalLL > bestLL {
			bestLL = totalLL
			opt.saveBest(crf)
		}

		if llChange < opt.config.Tol {
			if opt.config.Verbose {
				fmt.Printf("Converged at iteration %d\n", opt.iter)
			}
			break
		}

		opt.iter++
	}

	opt.restoreBest(crf)
	return crf, nil
}

func (opt *Optimizer) updateWeights(crf *model.CRF, gradE map[uint64]float64, gradT [][]float64) {
	lr := opt.config.LearningRate
	l2 := opt.config.L2Lambda

	switch opt.config.Type {
	case OptimizerSGD:
		opt.updateSGD(crf, gradE, gradT, lr, l2)
	case OptimizerMomentum:
		opt.updateMomentum(crf, gradE, gradT, lr, l2)
	case OptimizerAdam:
		opt.updateAdam(crf, gradE, gradT, lr, l2)
	default:
		opt.updateSGD(crf, gradE, gradT, lr, l2)
	}
}

func (opt *Optimizer) updateSGD(crf *model.CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	for key, grad := range gradE {
		w := crf.Weights.W[key]
		w += lr * (grad - l2*w)
		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			w += lr * (gradT[i][j] - l2*w)
			crf.Transition[i][j] = w
		}
	}
}

func (opt *Optimizer) updateMomentum(crf *model.CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	mom := opt.config.Momentum

	for key, grad := range gradE {
		w := crf.Weights.W[key]
		vel := opt.mVelEmission[key]
		vel = mom*vel - lr*(grad-l2*w)
		opt.mVelEmission[key] = vel
		w += vel
		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			vel := opt.mVelTransition[i][j]
			vel = mom*vel - lr*(gradT[i][j]-l2*w)
			opt.mVelTransition[i][j] = vel
			w += vel
			crf.Transition[i][j] = w
		}
	}
}

func (opt *Optimizer) updateAdam(crf *model.CRF, gradE map[uint64]float64, gradT [][]float64, lr, l2 float64) {
	beta1 := opt.config.Beta1
	beta2 := opt.config.Beta2
	eps := opt.config.Epsilon

	opt.iter++

	for key, grad := range gradE {
		w := crf.Weights.W[key]
		m := opt.mVelEmission[key]
		m = beta1*m + (1-beta1)*grad
		opt.mVelEmission[key] = m

		v := opt.vEmission[key]
		v = beta2*v + (1-beta2)*grad*grad
		opt.vEmission[key] = v

		mHat := m / (1 - math.Pow(beta1, float64(opt.iter)))
		vHat := v / (1 - math.Pow(beta2, float64(opt.iter)))

		w += lr * (mHat/(math.Sqrt(vHat)+eps) - l2*w)

		if math.Abs(w) < 1e-10 {
			delete(crf.Weights.W, key)
		} else {
			crf.Weights.W[key] = w
		}
	}

	L := len(crf.Transition)
	for i := 0; i < L; i++ {
		for j := 0; j < L; j++ {
			if crf.Transition[i][j] <= -1e8 {
				continue
			}
			w := crf.Transition[i][j]
			grad := gradT[i][j]

			m := opt.mVelTransition[i][j]
			m = beta1*m + (1-beta1)*grad
			opt.mVelTransition[i][j] = m

			v := opt.vTransition[i][j]
			v = beta2*v + (1-beta2)*grad*grad
			opt.vTransition[i][j] = v

			mHat := m / (1 - math.Pow(beta1, float64(opt.iter)))
			vHat := v / (1 - math.Pow(beta2, float64(opt.iter)))

			w += lr * (mHat/(math.Sqrt(vHat)+eps) - l2*w)
			crf.Transition[i][j] = w
		}
	}
}
