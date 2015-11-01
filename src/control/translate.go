package control

import (
	"../model"
	// "code.google.com/p/go.crypto/bcrypt"
	// "crypto/md5"
	// "encoding/hex"
	// "html/template"
	"math"
	// "math/rand"
	"encoding/csv"
	// "io"
	"bufio"
	"fmt"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	PageSize = 50
)

func SourcesHandler(w http.ResponseWriter, r *http.Request) {
	renderTemplate("sources", w, r, func(data TemplateData) TemplateData {
		data.CurrentGame = r.FormValue("game")
		data.CurrentLevel = r.FormValue("level")
		data.CurrentShow = r.FormValue("show")
		data.CurrentSearch = r.FormValue("search")

		leveln, err := strconv.Atoi(data.CurrentLevel)
		if err != nil || leveln > 4 || leveln < 1 {
			leveln = 0
		}
		data.Sources = model.GetSourcesAt(data.CurrentGame, leveln, data.CurrentShow)
		fmt.Println("Writing", len(data.Sources), "sources")

		data.Page = Paginate(r, PageSize, len(data.Sources))
		data.Sources = data.Sources[data.Page.Offset:data.Page.Slice]
		return data
	})
}

func EntriesHandler(w http.ResponseWriter, r *http.Request) {
	currentUser := GetCurrentUser(r)
	renderTemplate("entries", w, r, func(data TemplateData) TemplateData {
		data.CurrentGame = r.FormValue("game")
		data.CurrentLevel = r.FormValue("level")
		data.CurrentShow = r.FormValue("show")
		data.CurrentSearch = r.FormValue("search")

		data.Entries = model.GetStackedEntries(data.CurrentGame, data.CurrentLevel, data.CurrentShow, data.CurrentSearch, "uses", "gb", currentUser)
		data.Page = Paginate(r, PageSize, len(data.Entries))
		data.Entries = data.Entries[data.Page.Offset:data.Page.Slice]
		return data
	})
}

func TranslationHandler(w http.ResponseWriter, r *http.Request) {
	currentUser := GetCurrentUser(r)
	renderTemplate("translate", w, r, func(data TemplateData) TemplateData {
		rlang := r.FormValue("language")
		if rlang != "" {
			data.CurrentLanguage = rlang
		}

		data.CurrentGame = r.FormValue("game")
		data.CurrentLevel = r.FormValue("level")
		data.CurrentShow = r.FormValue("show")
		data.CurrentSearch = r.FormValue("search")
		data.CurrentSort = r.FormValue("sort")
		if data.CurrentSort == "" {
			data.CurrentSort = "uses"
		}
		if data.CurrentSearch != "" {
			fmt.Println("Searching for:", data.CurrentSearch)
		}
		data.Entries = model.GetStackedEntries(data.CurrentGame, data.CurrentLevel, data.CurrentShow, data.CurrentSearch, data.CurrentSort, data.CurrentLanguage, currentUser)

		data.Page = Paginate(r, PageSize, len(data.Entries))
		data.Entries = data.Entries[data.Page.Offset:data.Page.Slice]
		return data
	})
}

var CurrentProgress map[int]*TaskProgress
var nextProgressID chan int

func init() {
	CurrentProgress = make(map[int]*TaskProgress)
	nextProgressID = make(chan int)
	go func () {
		i := 1
		for {
			i++
			nextProgressID <- i
		}
	}()
}

type TaskProgress struct {
	ID       int
	Progress int
	Scale    int
	Finished bool
	Abort    bool
}

func importMasterData(data []map[string]string, clean bool, progress *TaskProgress) {
	sleepTime, _ := time.ParseDuration("5ms")
	fmt.Println("Importing", len(data), "master records")
	progress.Scale = len(data) + len(data) / 4
	progress.Progress = 0

	if clean {
		if model.Debug >= 1 {
			fmt.Println("Clean import")
		}
		model.DeleteAllEntrySources()
	}

	for _, record := range data {
		if progress.Abort {
			fmt.Println("Import aborted")
			return
		}
		if model.Debug >= 2 {
			fmt.Println("Inserting translation:", record["Original"], ";", record["Part of"])
		}
		entry := &model.Entry{
			Original: record["Original"],
			PartOf:   record["Part of"],
		}
		entry.Save()

		filepath := record["File"]
		filename := path.Base(filepath)
		ext := path.Ext(filepath)
		name := strings.TrimSuffix(filename, ext)
		level, _ := strconv.Atoi(record["Level"])
		source := &model.Source{
			Filepath: filepath,
			Page:     name,
			Volume:   record["Volume"],
			Level:    level,
			Game:     record["Game"],
		}
		source.Save()

		count, _ := strconv.Atoi(record["Count"])
		entrySource := &model.EntrySource{
			Entry:  *entry,
			Source: *source,
			Count:  count,
		}
		entrySource.Save()
		time.Sleep(sleepTime)
		progress.Progress++
	}
	fmt.Println("Import complete. Imported", progress.Progress, "master records")

	model.MarkAllConflicts()
	progress.Finished = true
	fmt.Println("Conflicts marked")
}

func importTranslationData(data []map[string]string, language string, translator *model.User, progress *TaskProgress) {
	sleepTime, _ := time.ParseDuration("5ms")
	fmt.Println("Importing", len(data), "translation records as", translator.Name)
	progress.Scale = len(data)
	progress.Progress = 0
	for _, record := range data {
		t := record["Translation"]
		if t == "" {
			progress.Scale--
			continue
		}
		translation := &model.Translation{
			Entry: model.Entry{
				Original: record["Original"],
				PartOf:   record["Part of"],
			},
			Language:    language,
			Translation: t,
			Translator:  translator.Email,
		}
		translation.Save(true)
		time.Sleep(sleepTime)
		progress.Progress++
	}
	fmt.Println("Import complete:", progress.Progress, "of", len(data))
	
	model.MarkAllConflicts()
	fmt.Println("Conflicts marked")
}

func ImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		fmt.Println("POST import")
		// clean := r.FormValue("clean-import") == "on"
		importType := r.FormValue("type")
		if importType != "master" && importType != "translations" {
			fmt.Println("Missing type")
			http.Redirect(w, r, "/import", 303)
			return
		}
		file, _, err := r.FormFile("import-file")
		if err != nil {
			fmt.Println("Error reading file:", err)
			http.Redirect(w, r, "/import", 303)
			return
		}
		if file == nil {
			fmt.Println("Missing file")
			http.Redirect(w, r, "/import", 303)
			return
		}

		file2 := stripBOM(file)
		lines, err := csv.NewReader(file2).ReadAll()
		if err != nil {
			fmt.Println("Error reading CSV:", err)
			http.Redirect(w, r, "/import", 303)
			return
		}
		file.Close()
		data := associateData(lines)
		fmt.Println("Found", len(data), "lines")

		progress := new(TaskProgress)
		progress.ID = <- nextProgressID
		CurrentProgress[progress.ID] = progress

		if importType == "master" {
			clean := r.FormValue("clean") == "on"
			go importMasterData(data, clean, progress)
		} else {
			language := r.FormValue("language")
			translator := model.GetUserByEmail(r.FormValue("translator"))
			go importTranslationData(data, language, translator, progress)
		}

		http.Redirect(w, r, "/import/progress?id="+strconv.Itoa(progress.ID), 303)
	} else {
		renderTemplate("import", w, r, func(data TemplateData) TemplateData {
			data.Users = model.GetUsers()
			return data
		})
	}
}

func ImportProgressHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.FormValue("id"))
	progress, ok := CurrentProgress[id]
	if ok && progress.Finished {
		http.Redirect(w, r, "/import", 303)
	}
	
	renderTemplate("import_progress", w, r, func(data TemplateData) TemplateData {
		if ok {
			percent := float64(progress.Progress) * 100 / float64(progress.Scale)
			data.ProgressPercent = int(math.Floor(percent))
			data.ProgressID = progress.ID
		}
		return data
	})
}

func ImportAbortHandler(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.FormValue("id"))
	if progress, ok := CurrentProgress[id]; ok {
		progress.Abort = true
	}
	http.Redirect(w, r, "/import", 303)
}

func ExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		language := r.FormValue("language")
		fmt.Println("Exporting in", language)
		translations := model.GetPreferredTranslations(language)

		w.Header().Set("Content-Encoding", "UTF-8")
		w.Header().Set("Content-Type", "application/csv; charset=UTF-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\""+model.LanguageNames[language]+".csv\"")

		out := csv.NewWriter(w)
		out.Write([]string{
			"Original",
			"Part of",
			"Translation",
		})
		for _, translation := range translations {
			for _, part := range translation.Parts {
				out.Write([]string{
					part.Entry.Original,
					part.Entry.PartOf,
					part.Translation,
				})
			}
		}
		out.Flush()
		return
	} else {
		renderTemplate("export", w, r, func(data TemplateData) TemplateData {
			return data
		})
	}
}

func LiveExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		fmt.Println("Exporting live translations")
		translations := model.GetLiveTranslations()

		w.Header().Set("Content-Encoding", "UTF-8")
		w.Header().Set("Content-Type", "application/csv; charset=UTF-8")
		w.Header().Set("Content-Disposition", "attachment; filename=\"live.csv\"")

		out := csv.NewWriter(w)
		out.Write([]string{
			"Original",
			"Part of",
			"Language",
			"Translation",
		})
		for _, translation := range translations {
			for _, part := range translation.Parts {
				out.Write([]string{
					part.Entry.Original,
					part.Entry.PartOf,
					translation.Language,
					part.Translation,
				})
			}
		}
		out.Flush()
		return
	}
}

func associateData(in [][]string) []map[string]string {
	out := make([]map[string]string, 0, len(in)-1)
	fields := in[0]
	linelen := len(fields)
	for i, line := range in {
		if i == 0 {
			continue
		}
		linedata := make(map[string]string, linelen)
		for j, value := range line {
			if value != "" {
				linedata[fields[j]] = value
			}
		}
		out = append(out, linedata)
	}
	return out
}

func stripBOM(file multipart.File) *bufio.Reader {
	br := bufio.NewReader(file)
	rune, _, _ := br.ReadRune()
	if rune != '\uFEFF' {
		br.UnreadRune() // Not a BOM -- put the rune back
	}
	return br
}
