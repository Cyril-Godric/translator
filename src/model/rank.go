package model

import (
	"fmt"
)

func (st *StackedTranslation) GetVotes() []*Vote {
	// entry := st.Entry.Entries[0]
	results := query("select "+voteFields+" from Votes where TranslationID = ?", st.Parts[0].ID()).rows(parseVote)

	votes := make([]*Vote, len(results))
	for i, result := range results {
		if vote, ok := result.(Vote); ok {
			votes[i] = &vote
		}
	}
	return votes
}

func GetPreferredTranslations(language string) []*StackedTranslation {
	lead := GetLanguageLead(language)
	var leadEmail string = ""
	if lead != nil {
		leadEmail = lead.Email
	}

	entries := stackEntries(GetEntries())
	pref := make([]*StackedTranslation, 0, len(entries))
	for _, entry := range entries {
		translations := entry.GetTranslations(language)
		selected := SelectPreferredTranslation(entry, language, translations, leadEmail)
		if selected != nil {
			pref = append(pref, selected)
		}
	}

	return pref
}

func SelectPreferredTranslation(entry *StackedEntry, language string, translations []*StackedTranslation, lead string) *StackedTranslation {
	if len(translations) == 0 {
		return nil
	}
	if len(translations) == 1 {
		return translations[0]
	}

	//  count scores for the text of a translation, so duplicates are merged
	//  votes are worth two; language lead is worth one (so it's a tie-breaker)
	scores := make(map[string]int, len(translations))

	for _, st := range translations {
		text := st.FullText
		scores[text] = 0
		votes := st.GetVotes()
		for _, vote := range votes {
			if vote.Vote {
				scores[text] += 2
			} else {
				scores[text] -= 2
			}
		}
		if st.Translator == lead {
			scores[text]++
		}
	}

	//  get translations from people who haven't voted

	//  pick the highest score
	highestText := ""
	highestScore := 0
	for text, score := range scores {
		if score > highestScore {
			highestScore = score
			highestText = text
		}
	}
	for _, st := range translations {
		if st.FullText == highestText {
			return st
		}
	}
	return translations[0]
}

type RankTranslation struct {
	Translation *StackedTranslation
	Rank        int
}

func (entry *StackedEntry) RankTranslations(translations []*StackedTranslation, save bool) []RankTranslation {
	if len(translations) == 0 {
		return nil
	}

	language := translations[0].Language
	lead := GetLanguageLead(language)

	ln := len(translations)

	// count votes
	scores := make(map[string]int, ln)
	upvoters := make(map[string][]string, ln)
	downvoters := make(map[string][]string, ln)

	for _, translation := range translations {
		scores[translation.FullText] = 0
		upvoters[translation.FullText] = make([]string, 0, ln)
		downvoters[translation.FullText] = make([]string, 0, ln)
	}

	for _, translation := range translations {
		upvoters[translation.FullText] = append(upvoters[translation.FullText], translation.Translator)

		for _, vote := range translation.GetVotes() {
			if vote.Vote {
				upvoters[translation.FullText] = append(upvoters[translation.FullText], vote.Voter.Email)
			} else {
				downvoters[translation.FullText] = append(downvoters[translation.FullText], vote.Voter.Email)
			}
		}
	}

	for text, ups := range upvoters {
		for _, voter := range ups {
			voteWeight := 2
			if lead != nil && voter == lead.Email {
				voteWeight++;
			}

			scores[text] += voteWeight
		}

		for _, voter := range downvoters[text] {
			voteWeight := 2
			if lead != nil && voter == lead.Email {
				voteWeight++;
			}

			scores[text] += voteWeight
		}
	}

	fmt.Println("Voting scores:", scores)

	// get translations from people who haven't upvoted
	// for _, translation := range translations {
	// 	if !hasUpvoted[translation.Translator] {
	// 		voteWeight := 2
	// 		if lead != nil && translation.Translator == lead.Email {
	// 			voteWeight++;
	// 		}
	// 		scores[translation.FullText] += voteWeight
	// 	}
	// }

	// find the highest rank
	topScore := 0
	topScoringText := ""
	for text, score := range scores {
		if score > topScore {
			topScore = score
			topScoringText = text
		}
	}

	// check if more than one translation has near that score
	threshold := topScore - 1
	numNearTopRank := 0
	for _, score := range scores {
		if score >= threshold {
			numNearTopRank++
		}
	}
	isConflicted := numNearTopRank > 1
	if isConflicted {
		fmt.Println("Conflict!", numNearTopRank, "translations for:", entry.FullText)
	}

	// update their flags
	for _, translation := range translations {
		translation.IsConflicted = isConflicted && scores[translation.FullText] >= threshold
		translation.IsPreferred = !isConflicted && translation.FullText == topScoringText

		for _, part := range translation.Parts {
			part.IsConflicted = translation.IsConflicted
			part.IsPreferred = translation.IsPreferred
			if save {
				fmt.Println("Saving translation part:", part)
				part.Save()
			}
		}
	}

	// return the marked and ranked translations
	ranked := make([]RankTranslation, len(translations))
	for i, translation := range translations {
		score := scores[translation.FullText]
		ranked[i] = RankTranslation{translation, score}
	}
	return ranked
}


func (se *StackedEntry) MarkConflicts(language string) {
	if Debug >= 1 {
		fmt.Println("Marking conflicts in '"+se.FullText+"'")
	}
	translations := se.GetTranslations(language)
	se.RankTranslations(translations, true)
}


func MarkAllConflicts() {
	stackedEntries := GetStackedEntries("", "", "", "", "", nil)
	fmt.Println("Loaded", len(stackedEntries), "stacked entries")

	for _, se := range stackedEntries {
		for _, lang := range Languages {
			se.MarkConflicts(lang)
		}
	}
}