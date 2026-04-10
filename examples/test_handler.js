context.JSON({
    "method": context.Method(),
    "path": context.Path(),
    "env_var": process.env.APP_TEST_VAR,
    "set_var": process.env.APP_GLOBAL_SET
});
