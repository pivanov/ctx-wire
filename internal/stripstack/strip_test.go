package stripstack

import "testing"

func TestStrip(t *testing.T) {
	javaIn := "Exception in thread \"main\" java.lang.NullPointerException\n" +
		"\tat com.example.App.handle(App.java:42)\n" +
		"\tat com.example.App.main(App.java:18)\n" +
		"\tat java.base/jdk.internal.reflect.NativeMethodAccessorImpl.invoke0(Native Method)\n" +
		"\tat java.base/jdk.internal.reflect.NativeMethodAccessorImpl.invoke(NativeMethodAccessorImpl.java:62)\n" +
		"\tat java.base/java.lang.reflect.Method.invoke(Method.java:566)\n" +
		"\tat sun.launcher.LauncherHelper.executeMainClass(LauncherHelper.java:765)"
	javaWant := "Exception in thread \"main\" java.lang.NullPointerException\n" +
		"\tat com.example.App.handle(App.java:42)\n" +
		"\tat com.example.App.main(App.java:18)\n" +
		"\t... (+4 library frames hidden)"

	causedByIn := "java.lang.RuntimeException: wrap\n" +
		"\tat com.example.App.main(App.java:10)\n" +
		"\tat java.base/java.lang.reflect.Method.invoke(Method.java:1)\n" +
		"\tat sun.reflect.X.y(X.java:2)\n" +
		"Caused by: java.io.IOException: disk\n" +
		"\tat com.example.Disk.read(Disk.java:5)\n" +
		"\tat java.base/java.io.FileInputStream.read(FileInputStream.java:6)\n" +
		"\tat java.base/java.io.FileInputStream.read2(FileInputStream.java:7)"
	causedByWant := "java.lang.RuntimeException: wrap\n" +
		"\tat com.example.App.main(App.java:10)\n" +
		"\t... (+2 library frames hidden)\n" +
		"Caused by: java.io.IOException: disk\n" +
		"\tat com.example.Disk.read(Disk.java:5)\n" +
		"\t... (+2 library frames hidden)"

	tests := []struct {
		name    string
		in      string
		want    string
		changed bool
	}{
		{
			name: "python collapses consecutive site-packages frames, keeps app frames + header + message",
			in: `Traceback (most recent call last):
  File "/app/main.py", line 10, in <module>
    run()
  File "/app/service.py", line 22, in run
    client.fetch()
  File "/usr/lib/python3.11/site-packages/requests/api.py", line 59, in get
    return request("get", url)
  File "/usr/lib/python3.11/site-packages/requests/sessions.py", line 587, in request
    resp = self.send(prep)
  File "/usr/lib/python3.11/site-packages/urllib3/connectionpool.py", line 790, in urlopen
    raise MaxRetryError()
ConnectionError: Max retries exceeded`,
			want: `Traceback (most recent call last):
  File "/app/main.py", line 10, in <module>
    run()
  File "/app/service.py", line 22, in run
    client.fetch()
  ... (+3 library frames hidden)
ConnectionError: Max retries exceeded`,
			changed: true,
		},
		{
			name: "node collapses a run of node:internal + node_modules frames, keeps app frame",
			in: `Error: boom
    at Object.<anonymous> (/app/src/index.js:5:7)
    at Module._compile (node:internal/modules/cjs/loader:1254:14)
    at Module._extensions..js (node:internal/modules/cjs/loader:1308:10)
    at require (node:internal/modules/cjs/helpers:179:18)
    at Runner.runTests (/app/node_modules/jest-runner/build/run.js:88:3)
    at processTicksAndRejections (node:internal/process/task_queues:95:5)`,
			want: `Error: boom
    at Object.<anonymous> (/app/src/index.js:5:7)
    ... (+5 library frames hidden)`,
			changed: true,
		},
		{name: "java collapses jvm-runtime + reflection frames, keeps app frames", in: javaIn, want: javaWant, changed: true},
		{name: "caused-by chain is preserved and bounds the run", in: causedByIn, want: causedByWant, changed: true},
		{
			name: "app-only python trace is left untouched",
			in: `Traceback (most recent call last):
  File "/app/a.py", line 1, in <module>
    go()
  File "/app/b.py", line 2, in go
    boom()
ValueError: x`,
			want: `Traceback (most recent call last):
  File "/app/a.py", line 1, in <module>
    go()
  File "/app/b.py", line 2, in go
    boom()
ValueError: x`,
			changed: false,
		},
		{
			name: "a single library frame between app frames is kept (run < 2)",
			in: `Error: boom
    at Object.x (/app/src/i.js:1:1)
    at y (/app/node_modules/z/z.js:2:2)
    at Object.z (/app/src/j.js:3:3)`,
			want: `Error: boom
    at Object.x (/app/src/i.js:1:1)
    at y (/app/node_modules/z/z.js:2:2)
    at Object.z (/app/src/j.js:3:3)`,
			changed: false,
		},
		{
			name: "go backtrace is not recognized in v1 and passes through",
			in: `panic: boom
goroutine 1 [running]:
main.main()
	/usr/local/go/src/runtime/panic.go:10 +0x1d`,
			want: `panic: boom
goroutine 1 [running]:
main.main()
	/usr/local/go/src/runtime/panic.go:10 +0x1d`,
			changed: false,
		},
		{
			name:    "plain prose is untouched",
			in:      "build succeeded\n3 tests passed\nat the end of the run",
			want:    "build succeeded\n3 tests passed\nat the end of the run",
			changed: false,
		},
		{name: "empty input", in: "", want: "", changed: false},
		{
			name: "trailing newline is preserved",
			in: `Error: boom
    at Object.<anonymous> (/app/src/index.js:5:7)
    at a (/app/node_modules/x/x.js:1:1)
    at b (/app/node_modules/y/y.js:2:2)
`,
			want: `Error: boom
    at Object.<anonymous> (/app/src/index.js:5:7)
    ... (+2 library frames hidden)
`,
			changed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := Strip(tt.in)
			if changed != tt.changed {
				t.Fatalf("changed = %v, want %v\n--- got ---\n%s", changed, tt.changed, got)
			}
			if got != tt.want {
				t.Fatalf("output mismatch\n--- got ---\n%q\n--- want ---\n%q", got, tt.want)
			}
		})
	}
}

// TestStripAdversarial pins the false-positive / false-collapse cases that two
// adversarial reviewers found against the first (loose-substring) cut. Each input
// must be left UNTOUCHED: an application frame, or non-trace output, must never be
// hidden just because it resembles a library path.
func TestStripAdversarial(t *testing.T) {
	keep := []struct {
		name string
		in   string
	}{
		{
			"java app package containing 'reflect' is not hidden",
			"com.example.InternalError\n\tat com.example.reflect.SchemaMapper.map(SchemaMapper.java:100)\n\tat com.example.reflect.FieldAccessor.get(FieldAccessor.java:50)\n\tat com.example.App.main(App.java:10)",
		},
		{
			"java 'sun.' company namespace is not hidden",
			"payment failed\n\tat com.example.App.main(App.java:10)\n\tat sun.mycompany.payments.PaymentProcessor.process(PaymentProcessor.java:88)\n\tat sun.mycompany.payments.Gateway.submit(Gateway.java:55)",
		},
		{
			"java 'javax.' company namespace is not hidden",
			"validation failed\n\tat com.example.App.main(App.java:10)\n\tat javax.mycompany.validation.Validator.validate(Validator.java:30)\n\tat javax.mycompany.validation.Schema.check(Schema.java:15)",
		},
		{
			"java 'jdk.' (non-internal) company namespace is not hidden",
			"build failed\n\tat com.example.App.main(App.java:10)\n\tat jdk.mytools.compiler.Compiler.compile(Compiler.java:50)\n\tat jdk.mytools.compiler.Linker.link(Linker.java:30)",
		},
		{
			"kotlin company namespace (not stdlib runtime) is not hidden",
			"service down\n\tat com.backend.api.ServiceHandler.handle(ServiceHandler.kt:50)\n\tat kotlin.mycompany.core.Router.dispatch(Router.kt:30)\n\tat kotlin.mycompany.core.Server.run(Server.kt:20)",
		},
		{
			"python app under a '/lib/python-scripts/' path is not hidden",
			"Traceback (most recent call last):\n  File \"/app/main.py\", line 5, in <module>\n    helper()\n  File \"/home/user/lib/python-scripts/src/utils.py\", line 10, in transform\n    do_work()\n  File \"/home/user/lib/python-scripts/src/core.py\", line 50, in core_fn\n    inner()\nValueError: bad input",
		},
		{
			"python app under '/opt/lib/python/myservice' is not hidden",
			"Traceback (most recent call last):\n  File \"/opt/lib/python/myservice/api.py\", line 55, in handle_request\n    return self.process(data)\n  File \"/opt/lib/python/myservice/processor.py\", line 30, in process\n    result = self.transform(data)\nValueError: invalid data",
		},
		{
			"node app dir containing 'node:' in its name is not hidden",
			"connection refused\n    at connect (/app/src/db.js:5:10)\n    at startServer (/srv/node:api/src/handler.js:42:7)\n    at bootstrap (/srv/node:api/src/bootstrap.js:10:3)",
		},
		{
			"k8s/networking 'at node:<ip>:port:0' log lines are not hidden",
			"Connection attempt log:\nat node:10.0.1.5:8080:0\nat node:10.0.1.6:8080:0\nat node:10.0.1.7:8080:0\nAll nodes checked",
		},
		{
			"k8s 'at node:<hostname>:port:0' log lines (letter host) are not hidden",
			"Deploying to cluster:\n  at node:control-plane:6443:0\n  at node:worker-1:5001:0\n  at node:worker-2:5002:0\nReady to serve traffic",
		},
		{
			"grep/rg results referencing node_modules (no leading slash) are not hidden",
			"Search results for \"deprecated\":\nat node_modules/old-lib/index.js:42:10\nat node_modules/another/src/api.js:17:3\nFound 2 matches",
		},
		{
			"JPype mixed Python/Java trace does not swallow the Java app frame",
			"Traceback (most recent call last):\nFile \"/usr/lib/python3.11/site-packages/jpype/_core.py\", line 30, in startJVM\n\tat com.example.App.processRequest(App.java:42)\nFile \"/usr/lib/python3.11/site-packages/jpype/_jvmfinder.py\", line 100, in findJVM\n    return self._find()\nRuntimeError: JVM not found",
		},
		{
			"python app dir named 'python3-tools' is not hidden",
			"Traceback (most recent call last):\n  File \"/app/main.py\", line 1, in <module>\n    start()\n  File \"/srv/lib/python3-tools/worker.py\", line 10, in work\n    process()\n  File \"/srv/lib/python3-tools/utils.py\", line 5, in process\n    compute()\nValueError: bad",
		},
		{
			"python app under a versioned '/lib/python3.11/myapp' path is not hidden",
			"Traceback (most recent call last):\n  File \"/app/main.py\", line 1, in <module>\n    start()\n  File \"/home/dev/lib/python3.11/myapp/module.py\", line 10, in work\n    process()\n  File \"/home/dev/lib/python3.11/myapp/utils.py\", line 5, in process\n    compute()\nValueError: bad",
		},
		{
			"bare absolute 'at /.../node_modules/...:N:N' lines (no parens) are not collapsed",
			"Search results:\nat /usr/local/node_modules/old-pkg/index.js:42:10\nat /app/node_modules/lodash/array.js:100:5\nFound 2 matches",
		},
	}
	for _, tt := range keep {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := Strip(tt.in)
			if changed {
				t.Fatalf("expected NO change, but frames were collapsed\n--- got ---\n%s", got)
			}
			if got != tt.in {
				t.Fatalf("output altered though changed=false\n--- got ---\n%q\n--- in ---\n%q", got, tt.in)
			}
		})
	}

	// Known, documented v1 limitation: a monorepo whose own packages run from
	// under /node_modules/ DOES get collapsed (the node_modules convention is
	// ambiguous). The raw trace is spooled, so nothing is lost. Pin the behavior.
	t.Run("monorepo node_modules frames are collapsed (documented limitation)", func(t *testing.T) {
		in := "Error: handler crash\n    at process (/app/src/index.js:3:5)\n    at MyHandler (/work/node_modules/my-internal-app/src/handler.js:42:7)\n    at Router (/work/node_modules/my-router-app/src/router.js:88:5)"
		_, changed := Strip(in)
		if !changed {
			t.Fatal("expected node_modules frames to collapse (documented limitation)")
		}
	})
}

func TestEnabledEnvOverride(t *testing.T) {
	SetEnabled(false)
	if Enabled() {
		t.Fatal("configured default off should be off")
	}
	t.Setenv("CTX_WIRE_STRIP_STACKTRACES", "1")
	if !Enabled() {
		t.Fatal("env=1 should enable over a false default")
	}
	t.Setenv("CTX_WIRE_STRIP_STACKTRACES", "off")
	SetEnabled(true)
	if Enabled() {
		t.Fatal("env=off should disable over a true default")
	}
	t.Setenv("CTX_WIRE_STRIP_STACKTRACES", "")
	if !Enabled() {
		t.Fatal("configured default on, env unset -> on")
	}
}
