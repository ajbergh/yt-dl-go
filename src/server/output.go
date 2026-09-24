package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

const defaultNamingPattern = "{channel} - {title} [{resolution}]"

var defaultUserCategories = []string{
	"Tech", "Science", "Coding", "Music", "Education", "Gaming", "Podcasts", "Archival", "General",
}

func defaultAppSettings() AppSettings {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = os.TempDir()
	}
	return AppSettings{
		DefaultQuality: "best", DefaultVideoStrategy: "best", MaxConcurrentDownloads: 3,
		DownloadLocation: filepath.Join(home, "Downloads", "YouTube_Vault"),
		NamingPattern:    defaultNamingPattern, SubfolderSorting: "channel",
		OutputFileMode: "0600", OutputFolderMode: "0700",
		DefaultCategory: "General", UserCategories: append([]string(nil), defaultUserCategories...),
		StorageMode: "managed-published",
	}
}

// mergeAppSettings supplies defaults for fields omitted by older clients or
// databases while retaining explicit user values.
func mergeAppSettings(defaults, settings AppSettings) AppSettings {
	if settings.DefaultQuality == "" {
		settings.DefaultQuality = defaults.DefaultQuality
	}
	if settings.DefaultVideoStrategy == "" {
		settings.DefaultVideoStrategy = defaults.DefaultVideoStrategy
	}
	if settings.MaxConcurrentDownloads == 0 {
		settings.MaxConcurrentDownloads = defaults.MaxConcurrentDownloads
	}
	if settings.DownloadLocation == "" {
		settings.DownloadLocation = defaults.DownloadLocation
	}
	if settings.NamingPattern == "" {
		settings.NamingPattern = defaults.NamingPattern
	}
	if settings.SubfolderSorting == "" {
		settings.SubfolderSorting = defaults.SubfolderSorting
	}
	if settings.DefaultCategory == "" {
		settings.DefaultCategory = defaults.DefaultCategory
	}
	if settings.UserCategories == nil {
		settings.UserCategories = append([]string(nil), defaults.UserCategories...)
	}
	if settings.StorageMode == "" {
		settings.StorageMode = defaults.StorageMode
	}
	if settings.OutputFileMode == "" {
		settings.OutputFileMode = defaults.OutputFileMode
	}
	if settings.OutputFolderMode == "" {
		settings.OutputFolderMode = defaults.OutputFolderMode
	}
	return settings
}

func resolveJobCategory(settings AppSettings, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		requested = strings.TrimSpace(settings.DefaultCategory)
	}
	for _, category := range settings.UserCategories {
		category = strings.TrimSpace(category)
		if strings.EqualFold(category, requested) {
			return category, nil
		}
	}
	return "", errors.New("category must match an active user category")
}

func validateAppSettings(settings AppSettings) error {
	location := strings.TrimSpace(settings.DownloadLocation)
	if location == "" || len(location) > 32760 || strings.ContainsRune(location, '\x00') || !filepath.IsAbs(location) {
		return errors.New("downloadLocation must be an absolute path")
	}
	cleanLocation := filepath.Clean(location)
	if filepath.Dir(cleanLocation) == cleanLocation {
		return errors.New("downloadLocation cannot be a drive or filesystem root")
	}
	if len(settings.NamingPattern) == 0 || len(settings.NamingPattern) > 240 || strings.ContainsAny(settings.NamingPattern, `/\\`) {
		return errors.New("namingPattern must be 1–240 characters and cannot contain path separators")
	}
	if !validNamingPattern(settings.NamingPattern) {
		return errors.New("namingPattern contains an unsupported token")
	}
	if _, err := parseOutputMode(settings.OutputFileMode); err != nil {
		return errors.New("outputFileMode must be a four-digit octal permission from 0000 to 0777")
	}
	if _, err := parseOutputMode(settings.OutputFolderMode); err != nil {
		return errors.New("outputFolderMode must be a four-digit octal permission from 0000 to 0777")
	}
	if !validVideoStrategy(settings.DefaultVideoStrategy) {
		return errors.New("defaultVideoStrategy must be best, compatibility, vp9, or av1")
	}
	if settings.SubfolderSorting != "channel" && settings.SubfolderSorting != "category" && settings.SubfolderSorting != "flat" {
		return errors.New("subfolderSorting must be channel, category, or flat")
	}
	if settings.StorageMode != "managed-published" && settings.StorageMode != "published-only" && settings.StorageMode != "managed-only" {
		return errors.New("storageMode must be managed-published, published-only, or managed-only")
	}
	if settings.BandwidthLimitBytesPerSec < 0 || settings.BandwidthLimitBytesPerSec > maxBandwidthLimitBytesPerSec {
		return errors.New("bandwidthLimitBytesPerSec must be between 0 and 1073741824")
	}
	if len(settings.UserCategories) == 0 || len(settings.UserCategories) > 50 {
		return errors.New("userCategories must contain between 1 and 50 categories")
	}
	seen := map[string]struct{}{}
	defaultCategoryExists := false
	for _, category := range settings.UserCategories {
		category = strings.TrimSpace(category)
		if category == "" || len([]rune(category)) > 40 || strings.ContainsAny(category, `/\\`) {
			return errors.New("each user category must be 1–40 characters and cannot contain path separators")
		}
		key := strings.ToLower(category)
		if _, exists := seen[key]; exists {
			return errors.New("userCategories cannot contain duplicates")
		}
		seen[key] = struct{}{}
		if strings.EqualFold(category, strings.TrimSpace(settings.DefaultCategory)) {
			defaultCategoryExists = true
		}
	}
	if !defaultCategoryExists {
		return errors.New("defaultCategory must match an active user category")
	}
	return nil
}

var namingTokens = []string{"{channel}", "{title}", "{resolution}", "{category}", "{id}", "{upload_date}", "{playlist}", "{index}", "{ext}", "{fps}", "{codec}"}

func validNamingPattern(pattern string) bool {
	if strings.ContainsAny(pattern, `/\\`) || strings.ContainsRune(pattern, '\x00') {
		return false
	}
	for i := 0; i < len(pattern); {
		if pattern[i] == '}' {
			return false
		}
		if pattern[i] != '{' {
			i++
			continue
		}
		end := strings.IndexByte(pattern[i:], '}')
		if end < 0 {
			return false
		}
		token := pattern[i : i+end+1]
		known := false
		for _, allowed := range namingTokens {
			if token == allowed {
				known = true
				break
			}
		}
		if !known {
			return false
		}
		i += end + 1
	}
	return true
}

func parseOutputMode(value string) (os.FileMode, error) {
	if len(value) != 4 || value[0] != '0' {
		return 0, errors.New("mode must use four octal digits")
	}
	parsed, err := strconv.ParseUint(value, 8, 16)
	if err != nil || parsed > 0777 {
		return 0, errors.New("mode must be between 0000 and 0777")
	}
	return os.FileMode(parsed), nil
}

type namingValues struct {
	Channel, Title, Resolution, Category string
	ID, UploadDate, Playlist, Index, Ext string
	FPS, Codec                           string
}

func expandNamingPattern(pattern string, values namingValues) string {
	tokens := map[string]string{
		"{channel}": values.Channel, "{title}": values.Title, "{resolution}": values.Resolution, "{category}": values.Category,
		"{id}": values.ID, "{upload_date}": values.UploadDate, "{playlist}": values.Playlist, "{index}": values.Index,
		"{ext}": values.Ext, "{fps}": values.FPS, "{codec}": values.Codec,
	}
	var output strings.Builder
	for i := 0; i < len(pattern); {
		if pattern[i] != '{' {
			output.WriteByte(pattern[i])
			i++
			continue
		}
		end := strings.IndexByte(pattern[i:], '}')
		if end < 0 {
			output.WriteString(pattern[i:])
			break
		}
		end += i + 1
		if value, ok := tokens[pattern[i:end]]; ok {
			output.WriteString(value)
			i = end
			continue
		}
		output.WriteString(pattern[i:end])
		i = end
	}
	return output.String()
}

func namingCodec(mimeType string) string {
	start := strings.Index(mimeType, `codecs="`)
	if start < 0 {
		return ""
	}
	start += len(`codecs="`)
	end := strings.IndexByte(mimeType[start:], '"')
	if end < 0 {
		return ""
	}
	codec := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType[start:start+end], ",", 2)[0]))
	switch {
	case strings.HasPrefix(codec, "avc1"):
		return "h264"
	case strings.HasPrefix(codec, "vp09"), strings.HasPrefix(codec, "vp9"):
		return "vp9"
	case strings.HasPrefix(codec, "av01"):
		return "av1"
	case strings.HasPrefix(codec, "mp4a"):
		return "aac"
	case strings.HasPrefix(codec, "opus"):
		return "opus"
	case strings.HasPrefix(codec, "mp3"):
		return "mp3"
	default:
		return codec
	}
}

func (s *server) publishOutput(j *jobState, file *mediaFile) error {
	if j.DownloadLocation == "" || j.NamingPattern == "" {
		return errors.New("job has no captured output preferences")
	}
	if !filepath.IsAbs(j.DownloadLocation) || filepath.Clean(j.DownloadLocation) != j.DownloadLocation {
		return errors.New("job contains an invalid output directory")
	}
	extension := filepath.Ext(file.Name)
	if extension != ".mp4" && extension != ".webm" && extension != ".mp3" && extension != ".m4a" {
		return errors.New("download has an unsupported output type")
	}
	channel := file.Author
	if strings.TrimSpace(channel) == "" {
		channel = "Unknown Channel"
	}
	title := file.Title
	if strings.TrimSpace(title) == "" {
		title = "Untitled"
	}
	category := j.Category
	if strings.TrimSpace(category) == "" {
		category = "General"
	}
	file.Category = category
	resolution := "audio"
	if file.Height > 0 {
		resolution = fmt.Sprintf("%dp", file.Height)
	}
	values := file.naming
	values.Channel, values.Title, values.Resolution, values.Category = channel, title, resolution, category
	values.Ext = strings.TrimPrefix(extension, ".")
	baseName := expandNamingPattern(j.NamingPattern, values)
	baseName = sanitizePathComponent(baseName)
	if baseName == "" {
		baseName = "Untitled"
	}
	if len([]rune(baseName)) > 170 {
		baseName = string([]rune(baseName)[:170])
	}
	folder := j.DownloadLocation
	switch j.SubfolderSorting {
	case "channel":
		folder = filepath.Join(folder, sanitizePathComponent(channel))
	case "category":
		folder = filepath.Join(folder, sanitizePathComponent(category))
	case "flat":
	default:
		return errors.New("job contains an unsupported folder sorting rule")
	}
	folderMode, err := parseOutputMode(j.OutputFolderMode)
	if err != nil {
		return fmt.Errorf("invalid captured output folder mode: %w", err)
	}
	if err := os.MkdirAll(folder, folderMode); err != nil {
		return fmt.Errorf("create download destination: %w", err)
	}
	// Apply the selected mode to directories created or reused for published files.
	if err := os.Chmod(folder, folderMode); err != nil {
		return fmt.Errorf("set download destination permissions: %w", err)
	}
	for suffix := 0; suffix < 10000; suffix++ {
		name := outputNameForPattern(baseName, extension, suffix)
		destination := filepath.Join(folder, name)
		if _, err := os.Lstat(destination); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect download destination: %w", err)
		}
		temporary, err := temporarySibling(destination)
		if err != nil {
			return fmt.Errorf("create temporary download output: %w", err)
		}
		temporaryPath := temporary.Name()
		if closeErr := temporary.Close(); closeErr != nil {
			_ = os.Remove(temporaryPath)
			return fmt.Errorf("close temporary download output: %w", closeErr)
		}
		_ = os.Remove(temporaryPath)

		publishedByMove := false
		if j.StorageMode == "published-only" {
			sourcePath := filepath.Join(j.dir, file.Name)
			moveErr := durableRename(sourcePath, temporaryPath)
			if moveErr == nil {
				fileMode, modeErr := parseOutputMode(j.OutputFileMode)
				if modeErr != nil {
					_ = durableRename(temporaryPath, sourcePath)
					return fmt.Errorf("invalid captured output file mode: %w", modeErr)
				}
				if modeErr = os.Chmod(temporaryPath, fileMode); modeErr != nil {
					_ = os.Chmod(temporaryPath, 0600)
					_ = durableRename(temporaryPath, sourcePath)
					return fmt.Errorf("set download file permissions: %w", modeErr)
				}
				if err := publishTemporary(temporaryPath, destination); err != nil {
					_ = os.Chmod(temporaryPath, 0600)
					_ = durableRename(temporaryPath, sourcePath)
					return fmt.Errorf("publish moved download output: %w", err)
				}
				publishedByMove = true
			} else if _, statErr := os.Lstat(temporaryPath); statErr == nil {
				_ = durableRename(temporaryPath, sourcePath)
				return fmt.Errorf("move download output: %w", moveErr)
			}
		}
		if !publishedByMove {
			source, openErr := openFinal(j.dir, file.Name)
			if openErr != nil {
				return openErr
			}
			temporary, copyErr := writeTemporaryCopy(source, folder, ".yt-dl-go-*.part", file.Size)
			closeErr := source.Close()
			if copyErr != nil || closeErr != nil {
				return fmt.Errorf("write temporary download output: %w", errors.Join(copyErr, closeErr))
			}
			fileMode, modeErr := parseOutputMode(j.OutputFileMode)
			if modeErr != nil {
				_ = os.Remove(temporary)
				return fmt.Errorf("invalid captured output file mode: %w", modeErr)
			}
			if modeErr = os.Chmod(temporary, fileMode); modeErr != nil {
				_ = os.Remove(temporary)
				return fmt.Errorf("set download file permissions: %w", modeErr)
			}
			if err := publishTemporary(temporary, destination); err != nil {
				if errors.Is(err, os.ErrExist) {
					_ = os.Remove(temporary)
					continue
				}
				_ = os.Remove(temporary)
				return fmt.Errorf("publish download output: %w", err)
			}
		}
		file.OutputName = name
		file.OutputPath = destination
		file.OutputRelativePath = filepath.ToSlash(filepath.Join(filepath.Base(folder), name))
		file.PublishedAvailable = true
		if j.SubfolderSorting == "flat" {
			file.OutputRelativePath = name
		}
		return nil
	}
	return errors.New("could not choose an unused output filename")
}

func outputNameForPattern(baseName, extension string, suffix int) string {
	outputExtension := extension
	if actualExtension := filepath.Ext(baseName); strings.EqualFold(actualExtension, extension) {
		baseName = strings.TrimSuffix(baseName, actualExtension)
		outputExtension = actualExtension
	}
	if suffix > 0 {
		baseName += fmt.Sprintf(" (%d)", suffix+1)
	}
	return baseName + outputExtension
}

func sanitizePathComponent(value string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(value) {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\\|?*`, r) {
			if !lastDash {
				builder.WriteByte('-')
			}
			lastDash = true
			continue
		}
		builder.WriteRune(r)
		lastDash = r == '-'
	}
	clean := strings.Trim(builder.String(), " .")
	if clean == "." || clean == ".." {
		return ""
	}
	device := strings.ToUpper(strings.SplitN(clean, ".", 2)[0])
	if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" ||
		(len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '1' && device[3] <= '9') {
		clean = "_" + clean
	}
	return clean
}

func validateOutputCopy(j *jobState, file mediaFile) error {
	if file.OutputPath == "" || file.OutputName == "" || file.OutputRelativePath == "" || !filepath.IsAbs(j.DownloadLocation) {
		return errors.New("job contains invalid output metadata")
	}
	relative := filepath.FromSlash(file.OutputRelativePath)
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("job contains an unsafe output path")
	}
	expected := filepath.Join(j.DownloadLocation, relative)
	if filepath.Clean(file.OutputPath) != expected || filepath.Base(file.OutputPath) != file.OutputName {
		return errors.New("job output path does not match its recorded destination")
	}
	return nil
}

func validateSubtitleOutputCopy(j *jobState, subtitle subtitleFile) error {
	if subtitle.OutputPath == "" || subtitle.OutputName == "" || subtitle.OutputRelativePath == "" || !filepath.IsAbs(j.DownloadLocation) {
		return errors.New("job contains invalid caption output metadata")
	}
	relative := filepath.FromSlash(subtitle.OutputRelativePath)
	if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("job contains an unsafe caption output path")
	}
	expected := filepath.Join(j.DownloadLocation, relative)
	if filepath.Clean(subtitle.OutputPath) != expected || filepath.Base(subtitle.OutputPath) != subtitle.OutputName {
		return errors.New("job caption output path does not match its recorded destination")
	}
	return validateSubtitleManagedName(subtitle.OutputName, subtitle.Format)
}

func removeOutputCopies(j *jobState) error {
	for _, file := range j.Files {
		if file.PublishedAvailable {
			if err := validateOutputCopy(j, file); err != nil {
				return err
			}
		}
		if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
			if err := validateSubtitleOutputCopy(j, *file.Subtitle); err != nil {
				return err
			}
		}
	}
	for index := range j.Files {
		file := &j.Files[index]
		if file.Subtitle != nil && file.Subtitle.PublishedAvailable {
			if err := os.Remove(file.Subtitle.OutputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			file.Subtitle.PublishedAvailable = false
		}
		if file.PublishedAvailable {
			if err := os.Remove(file.OutputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			file.PublishedAvailable = false
		}
		syncStoredFile(j, *file)
	}
	return nil
}

func removeManagedCopies(j *jobState) error {
	if err := os.RemoveAll(j.dir); err != nil {
		return err
	}
	for index := range j.Files {
		j.Files[index].ManagedAvailable = false
		j.Files[index].ThumbnailLocalAvailable = false
		if j.Files[index].Subtitle != nil {
			j.Files[index].Subtitle.ManagedAvailable = false
		}
	}
	for itemIndex, saved := range j.fileItems {
		saved.ManagedAvailable = false
		saved.ThumbnailLocalAvailable = false
		if saved.Subtitle != nil {
			saved.Subtitle.ManagedAvailable = false
		}
		j.fileItems[itemIndex] = saved
		if len(j.fileGroups[itemIndex]) == 0 {
			j.fileGroups[itemIndex] = []mediaFile{saved}
		} else {
			for groupIndex := range j.fileGroups[itemIndex] {
				groupFile := &j.fileGroups[itemIndex][groupIndex]
				groupFile.ManagedAvailable = false
				groupFile.ThumbnailLocalAvailable = false
				if groupFile.Subtitle != nil {
					groupFile.Subtitle.ManagedAvailable = false
				}
			}
		}
	}
	return nil
}
