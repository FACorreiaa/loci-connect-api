package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
)

// getPersonalizedPOIWithSemanticContext creates an enhanced prompt with semantic POI context
func (l *ServiceImpl) getPersonalizedPOIWithSemanticContext(interestNames []string, cityName, tagsPromptPart, userPrefs string, semanticPOIs []locitypes.POIDetailedInfo) string {
	var prompt strings.Builder
	prompt.WriteString(fmt.Sprintf(`
        Generate a personalized trip itinerary for %s, tailored to user interests [%s].

        **SEMANTIC CONTEXT - Consider these highly relevant POIs found via semantic search:**
        `, cityName, strings.Join(interestNames, ", ")))

	// Add semantic POI context
	if len(semanticPOIs) > 0 {
		prompt.WriteString("\n**Contextually Relevant POIs:**\n")
		for i, p := range semanticPOIs {
			if i >= 10 { // Limit context to avoid token overuse
				break
			}
			prompt.WriteString(fmt.Sprintf("- %s (%s): %s [Lat: %.6f, Lon: %.6f]\n",
				p.Name, p.Category, p.DescriptionPOI, p.Latitude, p.Longitude))
		}
		prompt.WriteString("\n**Instructions:** Use these semantic matches as inspiration and context. You may include them directly or use them to find similar places. Ensure variety and avoid exact duplicates.\n\n")
	}

	prompt.WriteString(`Include:
        1. An itinerary name that reflects both user interests and semantic context.
        2. An overall description highlighting semantic relevance.
        3. A list of points of interest with name, category, coordinates, and detailed description.
        Max points of interest allowed by tokens.

        **PRIORITIZATION:**
        - Highly weight POIs that align with the semantic context provided
        - Ensure semantic relevance in descriptions
        - Balance popular attractions with personalized semantic matches
        - Include variety across different categories while maintaining semantic coherence

        Format the response in JSON with the following structure:
        {
            "itinerary_name": "Name of the itinerary (reflecting semantic context)",
            "overall_description": "Description emphasizing semantic relevance to user interests",
            "points_of_interest": [
                {
                    "name": "POI name",
                    "latitude": latitude_as_number,
                    "longitude": longitude_as_number,
                    "category": "Category",
                    "description_poi": "Detailed description explaining semantic relevance to user interests and why this matches their preferences"
                }
            ]
        }`)

	if tagsPromptPart != "" {
		prompt.WriteString("\n**User Tags Context:** " + tagsPromptPart)
	}
	if userPrefs != "" {
		prompt.WriteString("\n**User Preferences:** " + userPrefs)
	}

	return prompt.String()
}

func (l *ServiceImpl) FetchUserData(ctx context.Context, userID, profileID uuid.UUID) (interests []*locitypes.Interest, searchProfile *locitypes.UserPreferenceProfileResponse, tags []*locitypes.Tags, err error) {
	// If no profile ID is provided, fall back to the user's default search profile.
	if profileID == uuid.Nil {
		searchProfile, err = l.searchProfileRepo.GetDefaultSearchProfile(ctx, userID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to fetch default search profile: %w", err)
		}
		if searchProfile != nil {
			profileID = searchProfile.ID
		}
	}

	interests, err = l.interestRepo.GetInterestsForProfile(ctx, profileID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch user interests: %w", err)
	}
	if searchProfile == nil {
		searchProfile, err = l.searchProfileRepo.GetSearchProfile(ctx, userID, profileID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to fetch search profile: %w", err)
		}
	}
	tags, err = l.tagsRepo.GetTagsForProfile(ctx, profileID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to fetch user tags: %w", err)
	}
	return interests, searchProfile, tags, nil
}

func (l *ServiceImpl) PreparePromptData(interests []*locitypes.Interest, tags []*locitypes.Tags, searchProfile *locitypes.UserPreferenceProfileResponse) (interestNames []string, tagsPromptPart, userPrefs string) {
	if len(interests) == 0 {
		interestNames = []string{"general sightseeing", "local experiences"}
	} else {
		for _, interest := range interests {
			if interest != nil {
				interestNames = append(interestNames, interest.Name)
			}
		}
	}
	var tagInfoForPrompt []string
	for _, tag := range tags {
		if tag != nil {
			tagDetail := tag.Name
			if tag.Description != nil && *tag.Description != "" {
				tagDetail += fmt.Sprintf(" (meaning: %s)", *tag.Description)
			}
			tagInfoForPrompt = append(tagInfoForPrompt, tagDetail)
		}
	}
	if len(tagInfoForPrompt) > 0 {
		tagsPromptPart = fmt.Sprintf("\n    - Additionally, consider these specific user tags/preferences: [%s].", strings.Join(tagInfoForPrompt, "; "))
	}
	userPrefs = getUserPreferencesPrompt(searchProfile)
	return interestNames, tagsPromptPart, userPrefs
}

// getEnhancedPersonalizedPOIPrompt creates a domain-aware prompt for personalized POI generation
func (l *ServiceImpl) getEnhancedPersonalizedPOIPrompt(cityName, enhancedPromptData string, domain locitypes.DomainType) string {
	domainFocus := ""
	switch domain {
	case locitypes.DomainAccommodation:
		domainFocus = "Focus particularly on accommodation recommendations and nearby attractions that complement the user's accommodation preferences."
	case locitypes.DomainDining:
		domainFocus = "Focus particularly on restaurant, food, and dining experiences that align with the user's culinary preferences."
	case locitypes.DomainActivities:
		domainFocus = "Focus particularly on activities, attractions, and experiences that match the user's activity preferences and physical capabilities."
	case locitypes.DomainItinerary:
		domainFocus = "Focus particularly on creating a well-structured itinerary that respects the user's planning style and pace preferences."
	default:
		domainFocus = "Provide a balanced mix of attractions, dining, and activities based on all user preferences."
	}

	prompt := fmt.Sprintf(`You are a travel AI assistant creating a personalized itinerary for %s.

User Preferences and Filters:
%s

Domain Focus: %s

%s

Create a comprehensive and personalized itinerary that heavily weighs the user's specific preferences and filters. Ensure that every recommendation aligns with their stated preferences.

Format the response in JSON with the following structure:
{
    "itinerary_name": "Personalized itinerary name reflecting user preferences",
    "overall_description": "Description emphasizing how this itinerary matches user preferences",
    "points_of_interest": [
        {
            "name": "POI name",
            "category": "Category",
            "coordinates": {
                "latitude": float64,
                "longitude": float64
            },
            "description": "Detailed description explaining why this POI matches the user's specific preferences and filters"
        }
    ]
}`, cityName, enhancedPromptData, domainFocus, getBasePersonalizedPromptInstructions())

	return prompt
}

func getBasePersonalizedPromptInstructions() string {
	return `
**Instructions:**
- Prioritize POIs that directly align with user preferences and filters
- Explain in descriptions how each POI matches their specific preferences
- Ensure variety while maintaining preference alignment
- Include practical details like accessibility if relevant to user preferences
- Consider user's pace and planning style preferences in the selection
- Maximum 8-10 POIs to maintain quality over quantity`
}

func getPersonalizedPOI(interestNames []string, cityName, tagsPromptPart, userPrefs string) string {
	prompt := fmt.Sprintf(`
        Generate a personalized trip itinerary for %s, tailored to user interests [%s]. Include:
        1. An itinerary name.
        2. An overall description.
        3. A list of points of interest with name, category, coordinates, and detailed description.
		Max points of interest allowed by tokens.
        Format the response in JSON with the following structure:
        {
            "itinerary_name": "Name of the itinerary",
            "overall_description": "Description of the itinerary",
            "points_of_interest": [
                {
                    "name": "POI name",
                    "category": "Category",
                    "coordinates": {
                        "latitude": float64,
                        "longitude": float64
                    },
                    "description": "Detailed description of why this POI matches the user's interests"
                }
            ]
        }
    `, cityName, strings.Join(interestNames, ", "))
	if tagsPromptPart != "" {
		prompt += "\n" + tagsPromptPart
	}
	if userPrefs != "" {
		prompt += "\n" + userPrefs
	}
	return prompt
}

// ContinueSessionStreamed handles subsequent messages in an existing session and streams responses/updates.
