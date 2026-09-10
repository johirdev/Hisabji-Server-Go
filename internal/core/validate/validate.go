// Package validate turns request binding failures into precise, friendly
// field errors. It is the reason an endpoint can answer
//
//	{"amount": "abc"}
//
// with
//
//	422 { "code": "VALIDATION_ERROR",
//	      "errors": [{ "field": "amount", "rule": "type",
//	                   "message": "amount must be a number." }] }
//
// instead of Go's default "json: cannot unmarshal string into Go struct field".
package validate

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/johirdev/Hisabji-Server/internal/core/apperr"
)

var initOnce sync.Once

// Init registers our JSON-aware field naming and the custom rules used by DTO
// tags across the app. Call once from main() before serving traffic.
func Init() {
	initOnce.Do(func() {
		v, ok := binding.Validator.Engine().(*validator.Validate)
		if !ok {
			return
		}

		// Report the JSON name ("user_image_path"), not the Go name.
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "-" || name == "" {
				return fld.Name
			}
			return name
		})

		_ = v.RegisterValidation("bdphone", isBDPhone)
		_ = v.RegisterValidation("username", isUsername)
		_ = v.RegisterValidation("strongpass", isStrongPassword)
		_ = v.RegisterValidation("notblank", isNotBlank)
		_ = v.RegisterValidation("hexcolor6", isHexColor)
		_ = v.RegisterValidation("safetext", isSafeText)
	})
}

// ---------------------------------------------------------------------------
// Custom rules
// ---------------------------------------------------------------------------

// Bangladeshi mobile: 01[3-9] + 8 digits, optionally +880 / 880 prefixed.
var bdPhoneRe = regexp.MustCompile(`^(?:\+?880|0)1[3-9]\d{8}$`)

func isBDPhone(fl validator.FieldLevel) bool {
	return bdPhoneRe.MatchString(strings.TrimSpace(fl.Field().String()))
}

// NormalizePhone converts any accepted Bangladeshi form to the canonical
// 01XXXXXXXXX shape, so the same person cannot register twice as +8801... and
// 01... . Unknown formats are returned trimmed and unchanged.
func NormalizePhone(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(s)
	switch {
	case strings.HasPrefix(s, "+880"):
		s = "0" + s[4:]
	case strings.HasPrefix(s, "880") && len(s) == 13:
		s = "0" + s[3:]
	}
	return s
}

var usernameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_.]{1,28})[a-z0-9]$`)

func isUsername(fl validator.FieldLevel) bool {
	s := strings.ToLower(strings.TrimSpace(fl.Field().String()))
	return usernameRe.MatchString(s) && !strings.Contains(s, "..") && !strings.Contains(s, "__")
}

// isStrongPassword requires 8+ chars with at least one letter and one digit.
// Deliberately not stricter: unusable rules push users toward "Password1!".
func isStrongPassword(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if len([]rune(s)) < 8 {
		return false
	}
	var hasLetter, hasDigit bool
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	return hasLetter && hasDigit
}

func isNotBlank(fl validator.FieldLevel) bool {
	return strings.TrimSpace(fl.Field().String()) != ""
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func isHexColor(fl validator.FieldLevel) bool {
	return hexColorRe.MatchString(fl.Field().String())
}

// isSafeText rejects control characters and angle brackets in free-text fields
// (notes, titles) as defence in depth against stored XSS in web clients.
func isSafeText(fl validator.FieldLevel) bool {
	s := fl.Field().String()
	if strings.ContainsAny(s, "<>") {
		return false
	}
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' && r != '\r' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Error translation
// ---------------------------------------------------------------------------

// Translate converts any binding/validation error into an *apperr.Error with
// one FieldError per problem.
func Translate(err error) *apperr.Error {
	if err == nil {
		return nil
	}
	if e, ok := apperr.As(err); ok {
		return e
	}

	// Empty body — by far the most common integration mistake.
	if errors.Is(err, io.EOF) {
		return apperr.New(400, apperr.CodeBadRequest, "A JSON request body is required.").
			WithHint("Send a JSON object and set Content-Type: application/json.").
			WithCause(err)
	}

	// Malformed JSON.
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return apperr.New(400, apperr.CodeBadRequest,
			fmt.Sprintf("The request body is not valid JSON (at byte %d).", syn.Offset)).
			WithHint("Check for a trailing comma, a missing quote or a missing brace.").
			WithCause(err)
	}

	// Right JSON, wrong type for a field.
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		want := friendlyType(typeErr.Type.String())
		return apperr.Validation("Some fields have the wrong type.",
			apperr.FieldError{
				Field: field, Rule: "type", Value: typeErr.Value,
				Message:   fmt.Sprintf("%s must be %s.", field, want),
				MessageBN: fmt.Sprintf("%s অবশ্যই %s হতে হবে।", field, want),
			}).WithCause(err)
	}

	// Failed validator tags.
	var verrs validator.ValidationErrors
	if errors.As(err, &verrs) {
		fields := make([]apperr.FieldError, 0, len(verrs))
		for _, fe := range verrs {
			fields = append(fields, describe(fe))
		}
		return apperr.Validation("Some fields are missing or invalid.", fields...).
			WithHint("Fix the listed fields and submit again.").
			WithCause(err)
	}

	// Anything else from the binder (bad form field, bad query type, ...).
	return apperr.New(400, apperr.CodeBadRequest, "The request could not be read.").WithCause(err)
}

// describe renders one validator failure as a human sentence in English and
// Bangla. Keeping both here means every module gets bilingual errors for free.
func describe(fe validator.FieldError) apperr.FieldError {
	field := fe.Field()
	param := fe.Param()
	out := apperr.FieldError{Field: fieldPath(fe), Rule: fe.Tag()}

	// Never echo a secret back to the caller.
	if !isSecretField(field) {
		out.Value = fe.Value()
	}

	switch fe.Tag() {
	case "required":
		out.Rule = "required"
		out.Message = fmt.Sprintf("%s is required.", field)
		out.MessageBN = fmt.Sprintf("%s দেওয়া আবশ্যক।", field)
	case "required_if", "required_with", "required_unless", "required_without":
		out.Message = fmt.Sprintf("%s is required in this context.", field)
		out.MessageBN = fmt.Sprintf("এই ক্ষেত্রে %s দেওয়া আবশ্যক।", field)
	case "notblank":
		out.Message = fmt.Sprintf("%s cannot be empty.", field)
		out.MessageBN = fmt.Sprintf("%s খালি রাখা যাবে না।", field)
	case "email":
		out.Message = fmt.Sprintf("%s must be a valid email address.", field)
		out.MessageBN = "সঠিক email ঠিকানা দিন।"
	case "bdphone":
		out.Message = "Enter a valid Bangladeshi mobile number, e.g. 01712345678."
		out.MessageBN = "সঠিক মোবাইল নম্বর দিন, যেমন ০১৭১২৩৪৫৬৭৮।"
	case "username":
		out.Message = "Username may use 3-30 lowercase letters, numbers, dots and underscores, and must start and end with a letter or number."
		out.MessageBN = "Username-এ ছোট হাতের অক্ষর, সংখ্যা, dot ও underscore ব্যবহার করা যাবে (৩-৩০ অক্ষর)।"
	case "strongpass":
		out.Message = "Password must be at least 8 characters and contain both a letter and a number."
		out.MessageBN = "Password কমপক্ষে ৮ অক্ষরের হতে হবে এবং অন্তত একটি অক্ষর ও একটি সংখ্যা থাকতে হবে।"
	case "min":
		out.Message = fmt.Sprintf("%s must be at least %s%s.", field, param, unitFor(fe))
		out.MessageBN = fmt.Sprintf("%s কমপক্ষে %s হতে হবে।", field, param)
	case "max":
		out.Message = fmt.Sprintf("%s must be at most %s%s.", field, param, unitFor(fe))
		out.MessageBN = fmt.Sprintf("%s সর্বোচ্চ %s হতে পারে।", field, param)
	case "len":
		out.Message = fmt.Sprintf("%s must be exactly %s characters.", field, param)
		out.MessageBN = fmt.Sprintf("%s ঠিক %s অক্ষরের হতে হবে।", field, param)
	case "gt":
		out.Message = fmt.Sprintf("%s must be greater than %s.", field, param)
		out.MessageBN = fmt.Sprintf("%s অবশ্যই %s-এর চেয়ে বড় হতে হবে।", field, param)
	case "gte":
		out.Message = fmt.Sprintf("%s must be %s or more.", field, param)
		out.MessageBN = fmt.Sprintf("%s অবশ্যই %s বা তার বেশি হতে হবে।", field, param)
	case "lt":
		out.Message = fmt.Sprintf("%s must be less than %s.", field, param)
		out.MessageBN = fmt.Sprintf("%s অবশ্যই %s-এর চেয়ে ছোট হতে হবে।", field, param)
	case "lte":
		out.Message = fmt.Sprintf("%s must be %s or less.", field, param)
		out.MessageBN = fmt.Sprintf("%s অবশ্যই %s বা তার কম হতে হবে।", field, param)
	case "oneof":
		out.Message = fmt.Sprintf("%s must be one of: %s.", field, strings.ReplaceAll(param, " ", ", "))
		out.MessageBN = fmt.Sprintf("%s এর মান হতে হবে: %s।", field, strings.ReplaceAll(param, " ", ", "))
	case "uuid", "uuid4":
		out.Message = fmt.Sprintf("%s must be a valid id.", field)
		out.MessageBN = fmt.Sprintf("%s একটি সঠিক id নয়।", field)
	case "url":
		out.Message = fmt.Sprintf("%s must be a valid URL.", field)
		out.MessageBN = "সঠিক URL দিন।"
	case "datetime":
		out.Message = fmt.Sprintf("%s must be a date in %s format.", field, param)
		out.MessageBN = fmt.Sprintf("%s তারিখটি %s format-এ দিতে হবে।", field, param)
	case "eqfield":
		out.Message = fmt.Sprintf("%s must match %s.", field, toSnake(param))
		out.MessageBN = fmt.Sprintf("%s এবং %s এক হতে হবে।", field, toSnake(param))
	case "nefield":
		out.Message = fmt.Sprintf("%s must be different from %s.", field, toSnake(param))
		out.MessageBN = fmt.Sprintf("%s এবং %s আলাদা হতে হবে।", field, toSnake(param))
	case "hexcolor6":
		out.Message = fmt.Sprintf("%s must be a hex colour such as #2E7D32.", field)
		out.MessageBN = "রঙ #2E7D32 এর মতো hex format-এ দিন।"
	case "safetext":
		out.Message = fmt.Sprintf("%s contains characters that are not allowed.", field)
		out.MessageBN = fmt.Sprintf("%s এ অননুমোদিত অক্ষর আছে।", field)
	case "alphanum":
		out.Message = fmt.Sprintf("%s may contain letters and numbers only.", field)
		out.MessageBN = fmt.Sprintf("%s এ শুধু অক্ষর ও সংখ্যা থাকতে পারবে।", field)
	case "numeric":
		out.Message = fmt.Sprintf("%s must be a number.", field)
		out.MessageBN = fmt.Sprintf("%s অবশ্যই সংখ্যা হতে হবে।", field)
	case "dive":
		out.Message = fmt.Sprintf("One of the items in %s is invalid.", field)
		out.MessageBN = fmt.Sprintf("%s এর একটি item সঠিক নয়।", field)
	default:
		out.Message = fmt.Sprintf("%s is invalid.", field)
		out.MessageBN = fmt.Sprintf("%s সঠিক নয়।", field)
	}

	if fe.Tag() == "required" {
		out.Rule = "required"
	}
	return out
}

// fieldPath renders nested/indexed paths like "items[0].amount" so a client
// can highlight the exact row of a repeated form.
func fieldPath(fe validator.FieldError) string {
	ns := fe.Namespace()
	// Namespace looks like "CreateBudgetRequest.Limits[0].Amount"; drop the
	// struct name and lower-case each segment via the json tag mapping that
	// RegisterTagNameFunc already applied to Field().
	if i := strings.Index(ns, "."); i >= 0 {
		ns = ns[i+1:]
	}
	if ns == "" {
		return fe.Field()
	}
	segs := strings.Split(ns, ".")
	if len(segs) == 1 {
		return fe.Field()
	}
	segs[len(segs)-1] = fe.Field()
	return strings.Join(segs, ".")
}

// unitFor adds " characters" / " items" so "min 3" reads correctly for the
// kind of value that failed.
func unitFor(fe validator.FieldError) string {
	switch fe.Kind() {
	case reflect.String:
		return " characters"
	case reflect.Slice, reflect.Array, reflect.Map:
		return " items"
	default:
		return ""
	}
}

func friendlyType(goType string) string {
	switch {
	case strings.Contains(goType, "int"), strings.Contains(goType, "float"),
		strings.Contains(goType, "Amount"):
		return "a number"
	case strings.Contains(goType, "bool"):
		return "true or false"
	case strings.Contains(goType, "Time"):
		return "a date"
	case strings.HasPrefix(goType, "[]"):
		return "an array"
	case strings.Contains(goType, "string"):
		return "text"
	default:
		return "of type " + goType
	}
}

func isSecretField(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "password") || strings.Contains(n, "otp") ||
		strings.Contains(n, "token") || strings.Contains(n, "secret") ||
		strings.Contains(n, "pin")
}

func toSnake(s string) string {
	var sb strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				sb.WriteByte('_')
			}
			sb.WriteRune(r + 32)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}
