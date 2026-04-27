package parser_test

// Blank-importing types/internal in this external test file pulls types
// into the test binary, triggering its init() which registers the HM checker
// with the parser. Without this, parser tests would skip HM and miss type
// errors that we now expect Parse() to catch.
import _ "mar/internal/types"
