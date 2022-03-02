### Performance Test

This contains a binary which can run a series of tests on a homeserver implementation and uses `docker stats` to compare:
 - CPU usage
 - Memory usage
 - Network I/O
 - Block I/O

when performing these tests. Currently only 1 test is provided which:
 - Registers N users. Does not snapshot anything as cpu/memory/disk/time is intentionally high for things like bcrypt.
 - Creates M rooms concurrently. Tests how fast room creation is.
 - Joins X users to Y rooms according to a normal distribution. Tests how fast room joins are.
 - Sends messages randomly into these rooms. Tests how fast message sending is.
 - Syncs all users. Tests how fast initial syncs can be.
 - Changes the display name of all users.
 - Does an incremental sync on all users. Tests how fast notifier code is.

This is designed to simulate a small local-only homeserver with a few large rooms with lots of users and a few small rooms. The seed can be fixed
to ensure deterministic results.
