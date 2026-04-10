try {
    const email = 'john' + Math.floor(Math.random() * 1000) + '@example.com';
    var db = database.connection("db");
    const user = db.Model("user").create({
        name: 'John Doe',
        email: email,
        age: 30
    });

    const hello = user.greet("Love");
    const full = user.fullName;

    const found = await db.Model("user").findByEmail(email);
    console.log(JSON.stringify(Object.keys(found)))
    return {
        email: email,
        success: true,
        user: user,
        greeting: hello,
        fullName: full,
        foundName: found ? found.name : 'not found'
    };
} catch (e) {
    return {
        success: false,
        error: e.message
    }
}
