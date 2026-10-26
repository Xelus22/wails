package flags

type GenerateModule struct {
	Common
	Compiler  string `description:"Use a different go compiler to build, eg go1.15beta1"`
	Tags      string `description:"Build tags to pass to Go compiler. Must be quoted. Space or comma (but not both) separated"`
	Verbosity int    `name:"v" description:"Verbosity level (0 = quiet, 1 = normal, 2 = verbose)"`
}

type GenerateTemplate struct {
	Common
	Name     string `description:"Name of the template to generate"`
	Frontend string `description:"Frontend to use for the template"`
	Quiet    bool   `description:"Suppress output"`
}

type GenerateWebBindings struct {
	Common
	Compiler string `description:"Use a different go compiler to build, eg go1.22beta1"`
	Tags     string `description:"Build tags to pass to Go compiler. Must be quoted. Space or comma (but not both) separated"`
	Verbosity int   `name:"v" description:"Verbosity level (0 = quiet, 1 = normal, 2 = verbose)"`
	SkipVerify bool `description:"Skip cross-checking the WASM bindings against the desktop wailsjs bindings"`
	PrintJSON bool `description:"Print the WASM bindings catalog as JSON to stdout"`
}

func (c *GenerateWebBindings) Default() *GenerateWebBindings {
	return &GenerateWebBindings{
		Compiler: "go",
	}
}

func (c *GenerateModule) Default() *GenerateModule {
	return &GenerateModule{
		Compiler: "go",
	}
}
