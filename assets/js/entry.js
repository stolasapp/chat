// Entry point for esbuild bundling.
// Templui component scripts are resolved from the Go module cache
// via the TEMPLUI_PATH placeholder replaced at build time.
import "TEMPLUI_PATH/components/input/input.js";
import "TEMPLUI_PATH/components/popover/popover.js";
import "TEMPLUI_PATH/components/selectbox/selectbox.js";
import "TEMPLUI_PATH/components/dialog/dialog.js";
import "TEMPLUI_PATH/components/textarea/textarea.js";
import "./app.js";
