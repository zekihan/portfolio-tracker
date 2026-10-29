package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"
)

var errUnsupportedValidationRule = errors.New("unsupported validation rule")

var errTrailingJSON = errors.New("request body must contain exactly one JSON value")

type validationErrors []string

func (failures validationErrors) Error() string {
	return strings.Join(failures, "\n")
}

func decodeJSON(request *http.Request, destination any) error {
	decoder := json.NewDecoder(request.Body)

	err := decoder.Decode(destination)
	if err != nil {
		return err //nolint:wrapcheck // Decoder error text is part of the existing API response.
	}

	var trailing any

	err = decoder.Decode(&trailing)
	if !errors.Is(err, io.EOF) {
		if err != nil {
			return err //nolint:wrapcheck // Preserve decoder errors in validation responses.
		}

		return errTrailingJSON
	}

	return validateRequest(destination)
}

// Validate the small set of rules used by the API, keeping the existing error
// format so clients receive the same validation responses without Gin.
func validateRequest(destination any) error {
	value := reflect.ValueOf(destination)
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}

		value = value.Elem()
	}

	if value.Kind() != reflect.Struct {
		return nil
	}

	var failures validationErrors

	for index := range value.NumField() {
		fieldType := value.Type().Field(index)

		rules := fieldType.Tag.Get("validate")
		if rules == "" {
			continue
		}

		field := value.Field(index)
		if strings.HasPrefix(rules, "omitempty,") {
			if field.IsZero() {
				continue
			}

			rules = strings.TrimPrefix(rules, "omitempty,")
		}

		for field.Kind() == reflect.Pointer {
			field = field.Elem()
		}

		valid, err := validateField(field, rules)
		if err != nil {
			return err
		}

		rule, _, _ := strings.Cut(rules, "=")

		if !valid {
			failures = append(
				failures,
				validationMessage(value.Type().Name(), fieldType.Name, rule),
			)
		}
	}

	if len(failures) != 0 {
		return failures
	}

	return nil
}

func validateField(field reflect.Value, rules string) (bool, error) {
	switch rules {
	case "required":
		return !field.IsZero(), nil
	case "gte=0":
		return field.Float() >= 0, nil
	case "min=1":
		return utf8.RuneCountInString(field.String()) >= 1, nil
	case "oneof=TRY USD EUR":
		return supportedCurrency(field.String()), nil
	case "oneof=Stock ETF 'TR Fund' Crypto Gold Cash":
		return slices.Contains([]string{"Stock", "ETF", "TR Fund", "Crypto", "Gold", "Cash"},
			field.String()), nil
	default:
		return false, fmt.Errorf("%w: %s", errUnsupportedValidationRule, rules)
	}
}

func validationMessage(typeName, fieldName, rule string) string {
	key := fieldName
	if typeName != "" {
		key = typeName + "." + fieldName
	}

	return fmt.Sprintf("Key: '%s' Error:Field validation for '%s' failed on the '%s' tag",
		key, fieldName, rule)
}

// Preserve the size-limit status for streaming bodies as well as declared lengths.
func respondJSONDecodeError(writer http.ResponseWriter, err error) {
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		writeJSON(writer, http.StatusRequestEntityTooLarge,
			map[string]any{errorKey: "request body too large"})

		return
	}

	writeJSON(writer, http.StatusBadRequest, map[string]any{errorKey: err.Error()})
}
