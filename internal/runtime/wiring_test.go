package runtime_test

// Pull in the HM type checker so its init() registers with parser.Parse.
// Tests in this directory call parser.Parse and rely on the inferred query
// parameter types HM produces.
import _ "mar/internal/types"
