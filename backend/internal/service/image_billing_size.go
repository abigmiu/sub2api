package service

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const (
	ImageBillingSize1K = "1K"
	ImageBillingSize2K = "2K"
	ImageBillingSize4K = "4K"

	ImageSizeSourceOutput  = "output"
	ImageSizeSourceInput   = "input"
	ImageSizeSourceDefault = "default"
	ImageSizeSourceLegacy  = "legacy"
)

const (
	imageSizeMultiple   = 16
	imageMaxEdge        = 3840
	imageMaxAspectRatio = 3.0
	imageMinPixels      = 655_360
	imageMaxPixels      = 8_294_400
	imageMaxRatioError  = 0.01
)

var imageTierPixelBudget = map[string]int{
	ImageBillingSize1K: 1_572_864,
	ImageBillingSize2K: 4_194_304,
	ImageBillingSize4K: imageMaxPixels,
}

var commonImageSizePresets = map[string]map[string]string{
	ImageBillingSize1K: {
		"1:1":  "1024x1024",
		"3:2":  "1536x1024",
		"2:3":  "1024x1536",
		"16:9": "1280x720",
		"9:16": "720x1280",
		"4:3":  "1024x768",
		"3:4":  "768x1024",
		"21:9": "1280x544",
	},
	ImageBillingSize2K: {
		"1:1":  "2048x2048",
		"3:2":  "2160x1440",
		"2:3":  "1440x2160",
		"16:9": "2560x1440",
		"9:16": "1440x2560",
		"4:3":  "2048x1536",
		"3:4":  "1536x2048",
		"21:9": "2560x1088",
	},
	ImageBillingSize4K: {
		"1:1":  "2880x2880",
		"3:2":  "3456x2304",
		"2:3":  "2304x3456",
		"16:9": "3840x2160",
		"9:16": "2160x3840",
		"4:3":  "3200x2400",
		"3:4":  "2400x3200",
		"21:9": "3840x1600",
	},
}

type ImageBillingSizeResolution struct {
	BillingSize string
	InputSize   string
	OutputSize  string
	Source      string
	Breakdown   map[string]int
}

type imageRatio struct {
	width  float64
	height float64
}

func ClassifyImageBillingTier(size string) (string, bool) {
	trimmed := strings.TrimSpace(size)
	normalized := strings.ToLower(trimmed)
	switch normalized {
	case "", "auto":
		return "", false
	case "1k":
		return ImageBillingSize1K, true
	case "2k":
		return ImageBillingSize2K, true
	case "4k":
		return ImageBillingSize4K, true
	}

	normalizedSize, ok := normalizeImageSizeForBilling(trimmed)
	if !ok {
		return "", false
	}
	width, height, ok := parseImageBillingDimensions(normalizedSize)
	if !ok {
		return "", false
	}
	pixels := width * height
	switch {
	case pixels <= imageTierPixelBudget[ImageBillingSize1K]:
		return ImageBillingSize1K, true
	case pixels <= imageTierPixelBudget[ImageBillingSize2K]:
		return ImageBillingSize2K, true
	case pixels <= imageTierPixelBudget[ImageBillingSize4K]:
		return ImageBillingSize4K, true
	default:
		return "", false
	}
}

func NormalizeImageBillingTierOrDefault(size string) string {
	if tier, ok := ClassifyImageBillingTier(size); ok {
		return tier
	}
	return ImageBillingSize2K
}

func ResolveImageBillingSize(inputSize string, outputSizes []string) ImageBillingSizeResolution {
	inputSize = strings.TrimSpace(inputSize)
	outputSizes = compactTrimmedStrings(outputSizes)

	breakdown := map[string]int{}
	outputSize := firstDisplayImageOutputSize(outputSizes)
	outputTier := ""
	for _, output := range outputSizes {
		tier, ok := ClassifyImageBillingTier(output)
		if !ok {
			continue
		}
		breakdown[tier]++
		if imageTierRank(tier) > imageTierRank(outputTier) {
			outputTier = tier
		}
	}
	if outputTier != "" {
		return ImageBillingSizeResolution{
			BillingSize: outputTier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceOutput,
			Breakdown:   normalizeImageSizeBreakdown(breakdown),
		}
	}

	if tier, ok := ClassifyImageBillingTier(inputSize); ok {
		return ImageBillingSizeResolution{
			BillingSize: tier,
			InputSize:   inputSize,
			OutputSize:  outputSize,
			Source:      ImageSizeSourceInput,
		}
	}

	return ImageBillingSizeResolution{
		BillingSize: ImageBillingSize2K,
		InputSize:   inputSize,
		OutputSize:  outputSize,
		Source:      ImageSizeSourceDefault,
	}
}

func ApplyOpenAIImageBillingResolution(result *OpenAIForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func ApplyForwardImageBillingResolution(result *ForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func applyImageBillingResolution(
	billingSize *string,
	inputSize *string,
	outputSize *string,
	source *string,
	breakdown *map[string]int,
	resolved ImageBillingSizeResolution,
) {
	*billingSize = resolved.BillingSize
	*inputSize = resolved.InputSize
	*outputSize = resolved.OutputSize
	*source = resolved.Source
	*breakdown = resolved.Breakdown
}

func parseImageBillingDimensions(size string) (int, int, bool) {
	normalized := strings.NewReplacer("X", "x", "×", "x").Replace(strings.TrimSpace(size))
	parts := strings.Split(normalized, "x")
	if len(parts) != 2 {
		return 0, 0, false
	}
	width, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	height, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func normalizeImageSizeForBilling(size string) (string, bool) {
	width, height, ok := parseImageBillingDimensions(size)
	if !ok {
		return "", false
	}
	normalizedWidth, normalizedHeight := normalizeImageDimensions(width, height)
	return fmt.Sprintf("%dx%d", normalizedWidth, normalizedHeight), true
}

func normalizeImageDimensions(width int, height int) (int, int) {
	normalizedWidth := roundToMultiple(width, imageSizeMultiple)
	normalizedHeight := roundToMultiple(height, imageSizeMultiple)

	scaleToFit := func(scale float64) {
		normalizedWidth = floorToMultiple(float64(normalizedWidth)*scale, imageSizeMultiple)
		normalizedHeight = floorToMultiple(float64(normalizedHeight)*scale, imageSizeMultiple)
	}
	scaleToFill := func(scale float64) {
		normalizedWidth = ceilToMultiple(float64(normalizedWidth)*scale, imageSizeMultiple)
		normalizedHeight = ceilToMultiple(float64(normalizedHeight)*scale, imageSizeMultiple)
	}

	for i := 0; i < 4; i++ {
		maxEdge := normalizedWidth
		if normalizedHeight > maxEdge {
			maxEdge = normalizedHeight
		}
		if maxEdge > imageMaxEdge {
			scaleToFit(float64(imageMaxEdge) / float64(maxEdge))
		}

		if float64(normalizedWidth)/float64(normalizedHeight) > imageMaxAspectRatio {
			normalizedWidth = floorToMultiple(float64(normalizedHeight)*imageMaxAspectRatio, imageSizeMultiple)
		} else if float64(normalizedHeight)/float64(normalizedWidth) > imageMaxAspectRatio {
			normalizedHeight = floorToMultiple(float64(normalizedWidth)*imageMaxAspectRatio, imageSizeMultiple)
		}

		pixels := normalizedWidth * normalizedHeight
		if pixels > imageMaxPixels {
			scaleToFit(math.Sqrt(float64(imageMaxPixels) / float64(pixels)))
		} else if pixels < imageMinPixels {
			scaleToFill(math.Sqrt(float64(imageMinPixels) / float64(pixels)))
		}
	}

	return normalizedWidth, normalizedHeight
}

func getPresetRatioKey(width int, height int) (string, bool) {
	if width <= 0 || height <= 0 {
		return "", false
	}
	divisor := gcd(width, height)
	key := fmt.Sprintf("%d:%d", width/divisor, height/divisor)
	_, ok := commonImageSizePresets[ImageBillingSize1K][key]
	return key, ok
}

func calculateImageSizeForTier(tier string, ratio imageRatio) (string, bool) {
	presetKey, ok := getPresetRatioKey(int(ratio.width), int(ratio.height))
	if ok {
		if preset, exists := commonImageSizePresets[tier][presetKey]; exists {
			return preset, true
		}
	}

	targetRatio := ratio.width / ratio.height
	pixelBudget, ok := imageTierPixelBudget[tier]
	if !ok {
		return "", false
	}

	bestWidth := 0
	bestHeight := 0
	bestPixels := 0

	for width := imageSizeMultiple; width <= imageMaxEdge; width += imageSizeMultiple {
		idealHeight := float64(width) / targetRatio
		candidates := []int{
			floorToMultiple(idealHeight, imageSizeMultiple),
			ceilToMultiple(idealHeight, imageSizeMultiple),
		}

		for _, height := range candidates {
			if height < imageSizeMultiple || height > imageMaxEdge {
				continue
			}
			pixels := width * height
			if pixels > pixelBudget || pixels < imageMinPixels {
				continue
			}
			if maxFloat(float64(width)/float64(height), float64(height)/float64(width)) > imageMaxAspectRatio {
				continue
			}

			actualRatio := float64(width) / float64(height)
			ratioError := math.Abs(actualRatio-targetRatio) / targetRatio
			if ratioError > imageMaxRatioError {
				continue
			}
			if pixels > bestPixels {
				bestPixels = pixels
				bestWidth = width
				bestHeight = height
			}
		}
	}

	if bestPixels == 0 {
		return "", false
	}
	return fmt.Sprintf("%dx%d", bestWidth, bestHeight), true
}

func roundToMultiple(value int, multiple int) int {
	return maxInt(multiple, int(math.Round(float64(value)/float64(multiple)))*multiple)
}

func floorToMultiple(value float64, multiple int) int {
	return maxInt(multiple, int(math.Floor(value/float64(multiple)))*multiple)
}

func ceilToMultiple(value float64, multiple int) int {
	return maxInt(multiple, int(math.Ceil(value/float64(multiple)))*multiple)
}

func compactTrimmedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstDisplayImageOutputSize(outputSizes []string) string {
	for _, output := range outputSizes {
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func imageTierRank(tier string) int {
	switch strings.ToUpper(strings.TrimSpace(tier)) {
	case ImageBillingSize1K:
		return 1
	case ImageBillingSize2K:
		return 2
	case ImageBillingSize4K:
		return 3
	default:
		return 0
	}
}

func normalizeImageSizeBreakdown(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for _, tier := range []string{ImageBillingSize1K, ImageBillingSize2K, ImageBillingSize4K} {
		if count := in[tier]; count > 0 {
			out[tier] = count
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func SortedImageBillingBreakdownKeys(breakdown map[string]int) []string {
	keys := make([]string, 0, len(breakdown))
	for key := range breakdown {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := imageTierRank(keys[i]), imageTierRank(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	return keys
}

func gcd(a int, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	return a
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func maxFloat(left float64, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
