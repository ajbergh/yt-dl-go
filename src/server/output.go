package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
		DefaultQuality: "best", MaxConcurrentDownloads: 3,
		DownloadLocation: filepath.Join(home, "Downloads", "YouTube_Vault"),
		NamingPattern:    defaultNamingPattern, SubfolderSorting: "channel",
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
	template := settings.NamingPattern
	for _, token := range []string{"{channel}", "{title}", "{resolution}", "{category}"} {
		template = strings.ReplaceAll(template, token, "value")
	}
	if strings.ContainsAny(template, "{}") {
		return errors.New("namingPattern contains an unsupported token")
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
	baseName := j.NamingPattern
	for token, value := range map[string]string{
		"{channel}": channel, "{title}": title, "{resolution}": resolution, "{category}": category,
	} {
		baseName = strings.ReplaceAll(baseName, token, value)
	}
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
	if err := os.MkdirAll(folder, 0700); err != nil {
		return fmt.Errorf("create download destination: %w", err)
	}
	source, err := openFinal(j.dir, file.Name)
	if err != nil {
		return err
	}
	defer source.Close()

	for suffix := 0; suffix < 10000; suffix++ {
		name := baseName + extension
		if suffix > 0 {
			name = fmt.Sprintf("%s (%d)%s", baseName, suffix+1, extension)
		}
		destination := filepath.Join(folder, name)
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create download output: %w", err)
		}
		copied, copyErr := io.Copy(out, source)
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || copied != file.Size || syncErr != nil || closeErr != nil {
			_ = os.Remove(destination)
			return errors.New("could not write download output")
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
		for itemIndex, saved := range j.fileItems {
			if saved.ID == file.ID {
				saved.PublishedAvailable = file.PublishedAvailable
				if saved.Subtitle != nil && file.Subtitle != nil {
					saved.Subtitle.PublishedAvailable = file.Subtitle.PublishedAvailable
				}
				j.fileItems[itemIndex] = saved
				break
			}
		}
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
	}
	return nil
}
