//! Minimal volume-helper for agent delivery.
//!
//!   helper populate <path>   read stdin, write it to <path>, chmod 0755
//!   helper clean    <path>   remove <path> (idempotent; missing file is ok)
const std = @import("std");
const linux = std.os.linux;

fn die(msg: []const u8) noreturn {
    std.debug.print("helper: {s}\n", .{msg});
    linux.exit(1);
}

fn check(rc: usize, what: []const u8) usize {
    if (@as(isize, @bitCast(rc)) < 0) die(what);
    return rc;
}

fn cmdPopulate(path: [:0]const u8) void {
    const fd: i32 = @intCast(check(
        linux.open(path.ptr, .{ .ACCMODE = .WRONLY, .CREAT = true, .TRUNC = true }, 0o755),
        "open",
    ));
    var buf: [64 * 1024]u8 = undefined;
    while (true) {
        const n = check(linux.read(0, &buf, buf.len), "read");
        if (n == 0) break;
        var off: usize = 0;
        while (off < n) off += check(linux.write(fd, buf[off..].ptr, n - off), "write");
    }
    _ = check(linux.fchmod(fd, 0o755), "fchmod");
    _ = linux.close(fd);
}

fn cmdClean(path: [:0]const u8) void {
    const err = @as(isize, @bitCast(linux.unlink(path.ptr)));
    // ENOENT (-2) is fine: nothing to remove.
    if (err < 0 and err != -2) die("unlink");
}

pub fn main(init: std.process.Init.Minimal) !void {
    var it = std.process.Args.Iterator.init(init.args);
    _ = it.next(); // argv[0]
    const sub = it.next() orelse die("usage: helper <populate|clean> <path>");
    const path = it.next() orelse die("usage: helper <populate|clean> <path>");
    if (std.mem.eql(u8, sub, "populate")) {
        cmdPopulate(path);
    } else if (std.mem.eql(u8, sub, "clean")) {
        cmdClean(path);
    } else {
        die("unknown subcommand");
    }
}
