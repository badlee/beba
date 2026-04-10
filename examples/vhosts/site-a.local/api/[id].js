module.exports = {
  GET: function() {
    const id = context.Locals("param_id");
    context.JSON({
      success: true,
      user_id: id,
      message: "Cette route dynamique API est gérée par FsRouter."
    });
  }
}
