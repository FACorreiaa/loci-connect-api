-- +goose Up
-- +goose StatementBegin
-- A list item of type poi, restaurant or hotel may point at either table a
-- place lives in. 0032 tied poi to points_of_interest and restaurant/hotel to
-- llm_suggested_pois, but every client now saves the points_of_interest id
-- for all three (a restaurant from search is a POI row), so "Add to list" on a
-- restaurant or hotel failed with "Referenced restaurant ... does not exist".
-- Older items written against llm_suggested_pois stay valid.
CREATE OR REPLACE FUNCTION validate_list_item_content_type() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.content_type IN ('poi', 'restaurant', 'hotel') THEN
        IF NOT EXISTS (SELECT 1 FROM points_of_interest WHERE id = NEW.item_id)
           AND NOT EXISTS (SELECT 1 FROM llm_suggested_pois WHERE id = NEW.item_id) THEN
            RAISE EXCEPTION 'Referenced % with id % does not exist', NEW.content_type, NEW.item_id;
        END IF;
    ELSIF NEW.content_type = 'itinerary' THEN
        IF NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.item_id AND is_itinerary = true)
           AND NOT EXISTS (SELECT 1 FROM user_saved_itineraries WHERE id = NEW.item_id) THEN
            RAISE EXCEPTION 'Referenced itinerary with id % does not exist', NEW.item_id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Restores 0032's function.
CREATE OR REPLACE FUNCTION validate_list_item_content_type() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.content_type = 'poi' THEN
        IF NOT EXISTS (SELECT 1 FROM points_of_interest WHERE id = NEW.item_id) THEN
            RAISE EXCEPTION 'Referenced POI with id % does not exist', NEW.item_id;
        END IF;
    ELSIF NEW.content_type = 'restaurant' THEN
        IF NOT EXISTS (SELECT 1 FROM llm_suggested_pois WHERE id = NEW.item_id) THEN
            RAISE EXCEPTION 'Referenced restaurant with id % does not exist', NEW.item_id;
        END IF;
    ELSIF NEW.content_type = 'hotel' THEN
        IF NOT EXISTS (SELECT 1 FROM llm_suggested_pois WHERE id = NEW.item_id) THEN
            RAISE EXCEPTION 'Referenced hotel with id % does not exist', NEW.item_id;
        END IF;
    ELSIF NEW.content_type = 'itinerary' THEN
        IF NOT EXISTS (SELECT 1 FROM lists WHERE id = NEW.item_id AND is_itinerary = true) THEN
            IF NOT EXISTS (SELECT 1 FROM user_saved_itineraries WHERE id = NEW.item_id) THEN
                RAISE EXCEPTION 'Referenced itinerary with id % does not exist', NEW.item_id;
            END IF;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
