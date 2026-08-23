package validator

import "regexp"

// slugPattern -- DATABASE_SCHEMA.md §5.7 organizations.slug: "URL-friendly
// identifier; unik global; lowercase, alphanumeric, hyphen". Tidak boleh
// diawali/diakhiri hyphen atau hyphen ganda (URL yang jelek/ambigu).
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const maxSlugLength = 100 // VARCHAR(100), §5.7

// IsValidSlug memvalidasi format slug organisasi (S3-02/03).
func IsValidSlug(slug string) bool {
	return slug != "" && len(slug) <= maxSlugLength && slugPattern.MatchString(slug)
}
