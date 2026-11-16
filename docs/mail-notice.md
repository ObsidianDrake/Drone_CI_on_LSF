# Build mail notices

In **Repo → Settings → Project Settings**, enable **Mail notice** and save.
The default is disabled. Repository settings permissions apply.
Click **Advanced settings** to reveal **To** and **CC**. This section starts
collapsed; collapsing it does not clear or disable saved recipients.

| Field | Blank value | Custom value |
| --- | --- | --- |
| To | Commit author's email | Replaces the author with the specified recipients |
| CC | No copies | Additional recipients |

Separate multiple To/CC addresses with commas. Invalid addresses or header
newlines are rejected when saving. Clear both fields to restore defaults.
The commit author is not necessarily the person who pushed the commit.
If no To is specified and the commit author has no email, notification is skipped
with a server warning.

## Server configuration

Configure these deployment environment variables (do not commit internal values):

```sh
DRONE_SMTP_HOST=smtp.example.com
DRONE_SMTP_PORT=2525
DRONE_SMTP_FROM=drone@example.com
```

The port defaults to 25. The relay uses plain SMTP, no credentials and no STARTTLS,
with a 30-second connection timeout and a 30-second SMTP transaction deadline.
The sender always uses `DRONE_SMTP_FROM`; repositories cannot override it.
Use a trusted internal SMTP relay that allows this sender address.

Mail is English UTF-8 plain text and includes repository, build number, branch,
status, commit, commit author, duration, and a clickable build URL. The URL uses
Drone's configured server address, including its base path.

A completed build emits one notification attempt for success, failure, error,
or cancellation, rather than one per step or pipeline. Pending approval and
Decline do not send mail. Missing SMTP configuration and delivery errors are
logged without changing the build result or suppressing existing webhooks.

A database claim prevents repeat attempts for the same build, including duplicate
events after a restart. This is best-effort delivery, not a durable mail queue:
a crash after the claim or an SMTP failure can lose the notice. Automatic replay
is deliberately avoided when SMTP acceptance is uncertain, to prevent duplicates.
No delivery receipt is implied by build success. Check server logs for
`mail notice: delivery failed or skipped` when troubleshooting.
