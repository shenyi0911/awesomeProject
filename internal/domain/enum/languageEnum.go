package enum

type Language string

const (
	LanguageJava   Language = "Java"
	LanguagePython Language = "Python"
	LanguageCpp    Language = "C++"
	LanguageCSharp Language = "C#"
	LanguageGo     Language = "Go"
)

var ValidLanguages = []Language{
	LanguageJava,
	LanguagePython,
	LanguageCpp,
	LanguageCSharp,
	LanguageGo,
}

func IsValidLanguage(l Language) bool {
	for _, v := range ValidLanguages {
		if v == l {
			return true
		}
	}
	return false
}
