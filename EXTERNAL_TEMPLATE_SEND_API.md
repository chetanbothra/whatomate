# External Template Send API

## Purpose

Use this endpoint when an external system wants Whatomate to send a WhatsApp template message directly.

This is different from `POST /api/messages/external`:

- `POST /api/messages/template` sends the message through WhatsApp
- `POST /api/messages/external` only stores a message that was already sent elsewhere

## Base URL

This document does not contain your server address. Every example below uses the placeholder:

```text
https://your-whatomate-server
```

Replace it with the address of the Whatomate installation you are integrating with. If you do not know it, ask the team that runs Whatomate — do not guess.

Typical values:

- production: `https://whatomate.yourcompany.com`
- local development on the same machine as Whatomate: `http://localhost:8080`

`http://localhost:8080` only works when your code runs on the same machine as Whatomate. From any other server it will fail.

The full endpoint URL is your base URL plus the endpoint path below.

## Endpoint

```http
POST /api/messages/template
```

## Authentication

Use a normal API key or an authenticated session.

Example:

```http
X-API-Key: whm_your_api_key
Content-Type: application/json
```

If you are using a super admin API key for another organization, also send:

```http
X-Organization-ID: <target-org-uuid>
```

## Simple Request Contract

This is the recommended payload shape for external systems:

```json
{
  "phone_number": "919876543210",
  "phone_number_id": "116800811516809",
  "template_name": "credentials_v1",
  "template_params": {
    "name": "Nitin Jain",
    "portal": "Income Tax"
  },
  "header_media_url": "https://cdn.example.com/files/credentials.pdf",
  "header_media_filename": "credentials.pdf",
  "status_callback_url": "https://billing.example.com/api/whatsapp-status",
  "status_callback_bearer_token": "your-secret-token"
}
```

## Required fields

- `phone_number` or `contact_id`
- `template_name` or `template_id`

## Recommended fields for external systems

- `phone_number`
- `phone_number_id`
- `template_name`
- `template_params` when the template has dynamic values
- `header_media_url` when the template uses header media
- `header_media_filename` when the template uses a document header and the displayed document name should be controlled
- `status_callback_url` when you need to know whether the message actually went out

## Field Notes

### Recipient

- `phone_number` sends directly to that number
- if the contact does not already exist, Whatomate creates it automatically
- `contact_id` can be used instead when the contact already exists in Whatomate

### WhatsApp account selection

Resolution priority is:

1. `phone_number_id`
2. `account_name`
3. the template's stored WhatsApp account
4. the contact's current WhatsApp account
5. the default outgoing account or fallback account

For external systems, `phone_number_id` is the simplest and safest option because it maps directly to the Meta phone number that should send the template.

### Template lookup

- `template_name` is the simplest option
- `template_id` can be used if your integration stores the local Whatomate template UUID
- the template must exist in Whatomate and be approved

### Dynamic parameters

Send dynamic values in `template_params`.

Named example:

```json
{
  "template_params": {
    "name": "Nitin Jain",
    "portal": "Income Tax"
  }
}
```

Positional example:

```json
{
  "template_params": {
    "1": "Nitin Jain",
    "2": "Income Tax"
  }
}
```

### Header media

If the template uses image, video, or document header media, you can send:

- `header_media_url`
- `header_media_id`
- multipart `header_file`
- `header_media_filename`

For most external systems, `header_media_url` is the simplest option.

### Document attachment name

For document-header templates, send the document name as:

```json
{
  "header_media_filename": "credentials.pdf"
}
```

Notes:

- use `header_media_filename`, not `document_name`, `file_name`, or `filename`
- with JSON requests, `header_media_filename` is used when Whatomate downloads `header_media_url` and uploads it to WhatsApp
- with multipart requests, `header_media_filename` can be sent as a normal text form field
- if `header_media_filename` is omitted for multipart, Whatomate uses the uploaded file part's filename
- if `header_media_filename` is omitted for `header_media_url`, Whatomate uses the basename from the URL path, for example `campaign-brief.pdf` from `https://cdn.example.com/files/campaign-brief.pdf?download=1`
- if `header_media_id` is used, Whatomate skips upload and uses the existing WhatsApp media ID; set the intended filename when that media is originally uploaded

## Common Examples

### 1. Minimal external send request

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "credentials_v1",
    "template_params": {
      "name": "Nitin Jain",
      "portal": "Income Tax"
    }
  }'
```

## Delivery Status Callback

The API response only confirms that Whatomate **accepted** your request. The message is sent to WhatsApp a moment later, in the background. If you need to know what actually happened to the message, supply a status callback URL.

This is optional. If you omit it, nothing changes and no callbacks are sent.

### How to request status updates

Add these two fields to your send request:

| Field | Required | Description |
|-------|----------|-------------|
| `status_callback_url` | No | Your endpoint. Whatomate POSTs a JSON body to it every time the status of this message changes. `https://` is strongly recommended; `http://` is accepted. |
| `status_callback_bearer_token` | No | Sent back to you as `Authorization: Bearer <token>` so you can verify the call came from Whatomate. |

`status_callback_url` must be an absolute URL — the full address including `https://` and the host name. `billing.example.com/hook` on its own is rejected with `400`.

**Your endpoint must be reachable from the public internet.** For security, Whatomate refuses to call private or internal addresses — `localhost`, `127.0.0.1`, `192.168.x.x`, `10.x.x.x`, `*.internal`, `*.local` and similar. Such a URL is accepted when you send it, but every callback to it will fail. If your system sits inside a private network, expose a public endpoint (or a tunnel) for callbacks, and ask the Whatomate operators before relying on an internal address.

### Send request with status callback

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "credentials_v1",
    "template_params": {
      "name": "Nitin Jain",
      "portal": "Income Tax"
    },
    "status_callback_url": "https://billing.example.com/api/whatsapp-status",
    "status_callback_bearer_token": "your-secret-token"
  }'
```

### Status callback sent by Whatomate

Whatomate POSTs this JSON body to `status_callback_url`:

```json
{
  "message_id": "12415f80-1d08-4c17-9030-08302bdf37a6",
  "whatsapp_message_id": "wamid.HBgMOTE5ODc2NTQzMjEwFQIAERgS",
  "status": "sent",
  "contact_id": "4c0a7f45-7d2f-4a7f-9a3c-1b0f6c4e5e20",
  "contact_phone": "919876543210",
  "contact_name": "Nitin Jain",
  "whatsapp_account": "Main Account",
  "template_name": "credentials_v1",
  "timestamp": "2026-06-09T10:30:00Z"
}
```

`message_id` is the same UUID returned as `id` in the original API response, so you can match the callback to your own record. Always match on `message_id`.

**Only `message_id`, `status` and `timestamp` are always present.** Every other field is left out of the JSON when Whatomate has no value for it — it is not sent as an empty string. In particular:

- `error_message` appears only when `status` is `failed`
- `whatsapp_message_id` is missing on a `failed` that happened before WhatsApp accepted the message, because no WhatsApp ID exists yet
- `contact_name` is missing when the contact was created automatically from `phone_number` and has never messaged you, because WhatsApp has not told us their profile name yet
- `template_name` is only present for template messages

`timestamp` is UTC, in ISO-8601 format.

Reply with any `2xx` status code. Whatomate ignores your response body. A non-`2xx` reply is treated as a failed callback: it is logged (along with the first 2 KB of your response, to help troubleshooting) and never retried.

### What each status means

| Status | Meaning | Business reading |
|--------|---------|------------------|
| `sent` | WhatsApp accepted the message. | On its way. **Not** proof the phone received it. |
| `delivered` | The message reached the recipient's device. | Actually arrived. |
| `read` | The recipient opened the message. | Seen by the person. |
| `failed` | The message did not go out, or WhatsApp rejected it after accepting it. | Nobody received it. Read `error_message` and act. |

Important points:

- `pending` appears in the immediate API response but is **never** sent as a callback. It only means "request accepted, not sent yet".
- `sent` is not delivery. A message can be `sent` and then turn `failed` minutes later — for example an invalid number, a blocked business, or an expired messaging window.
- `read` depends on the recipient's WhatsApp privacy settings. If they have read receipts turned off, WhatsApp never reports `read`, and you will only ever see `delivered`. This is a WhatsApp limitation, not a Whatomate one.
- Statuses only move forward. You will not receive `delivered` after `read`.
- Each status is normally sent once. WhatsApp can repeat its own notifications, so make your endpoint safe to call twice: record against `message_id` + `status` and ignore a repeat.
- `failed` can arrive at any point, including after `sent` or `delivered`.

**Do not bill, mark as delivered, or close a ticket on `sent`.** `sent` only means WhatsApp accepted the message from Whatomate. Treat `delivered` as the first business-grade proof that the message reached the customer, and `read` as extra information you may never receive.

### Typical sequences

- Normal: `sent` → `delivered` → `read`
- Recipient has read receipts off: `sent` → `delivered`
- Rejected by WhatsApp at send time: `failed` only
- Accepted then undeliverable: `sent` → `failed`

### Callback behaviour

- Callbacks are best effort. If your endpoint is down or returns an error, Whatomate logs the failure and moves on. There is no automatic retry. Instead, you can ask for the status yourself at any time — see [Checking Status On Demand](#checking-status-on-demand).
- A failing callback never affects the WhatsApp message itself.
- Each callback request is given 15 seconds to complete.
- If Whatomate is restarted while a callback is being delivered, that callback is lost and is never re-sent. Treat callbacks as a fast notification, not an accounting ledger — if a status never arrives for an important message, look it up on demand.
- Your token is stored with the message so that `delivered` and `read` callbacks arriving hours later can still be authenticated. Rotating your token therefore only affects **new** sends; messages already in flight keep calling you with the token they were sent with. Accept both values during a rotation window.
- The `sent` callback can arrive within milliseconds of your API call returning — sometimes before your own code has finished saving the API response. Match on `message_id` or `whatsapp_message_id` and handle callbacks arriving in any order.
- Errors that happen **before** the message is created — missing template, unknown phone number ID, bad parameters — are returned as an HTTP error on the original request. No callback is sent for those, because there is no message yet.
- Keep `status_callback_bearer_token` secret and rotate it if exposed.

## Checking Status On Demand

Because callbacks are never retried, you also have a way to simply ask. Use this whenever a callback did not arrive, after your own system was down, or as a nightly reconciliation of anything still unresolved.

You can use it with or without callbacks — it works for every message sent through this API, even ones sent without a `status_callback_url`.

### One message

```http
GET /api/messages/{id}/status
```

`{id}` accepts **either** the Whatomate message id (the `id` returned when you sent it) **or** the WhatsApp message id (`wamid...`) — whichever you happen to have stored.

```bash
curl "https://your-whatomate-server/api/messages/12415f80-1d08-4c17-9030-08302bdf37a6/status" \
  -H "X-API-Key: whm_your_api_key"
```

Response:

```json
{
  "status": "success",
  "data": {
    "message_id": "12415f80-1d08-4c17-9030-08302bdf37a6",
    "whatsapp_message_id": "wamid.HBgMOTE5ODc2NTQzMjEwFQIAERgS",
    "status": "delivered",
    "contact_id": "4c0a7f45-7d2f-4a7f-9a3c-1b0f6c4e5e20",
    "contact_phone": "919876543210",
    "contact_name": "Nitin Jain",
    "whatsapp_account": "Main Account",
    "template_name": "credentials_v1",
    "direction": "outgoing",
    "created_at": "2026-06-09T10:30:00Z",
    "updated_at": "2026-06-09T10:30:04Z"
  }
}
```

The field names are identical to the callback body, so the same code can handle both. `updated_at` is when the status last changed. `status` carries the same meanings described above, and can additionally be `pending` when the message has not left Whatomate yet.

### Many messages at once

```http
POST /api/messages/status
```

Ask about up to **100** messages in one call. Mix both kinds of id freely:

```bash
curl -X POST "https://your-whatomate-server/api/messages/status" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "message_ids": [
      "12415f80-1d08-4c17-9030-08302bdf37a6",
      "6d2b7e17-42a1-4f54-9b7a-2abf1b283a11"
    ],
    "whatsapp_message_ids": [
      "wamid.HBgMOTE5ODc2NTQzMjEwFQIAERgS"
    ]
  }'
```

Response:

```json
{
  "status": "success",
  "data": {
    "statuses": [
      { "message_id": "12415f80-1d08-4c17-9030-08302bdf37a6", "status": "delivered", "...": "..." },
      { "message_id": "6d2b7e17-42a1-4f54-9b7a-2abf1b283a11", "status": "failed", "error_message": "Template is paused", "...": "..." }
    ],
    "not_found": ["wamid.HBgMOTE5ODc2NTQzMjEwFQIAERgS"]
  }
}
```

Notes:

- Ids Whatomate does not recognise are listed in `not_found` instead of failing the whole request, so a reconciliation job can process what it can and flag the rest.
- Duplicate ids are returned once.
- Asking for more than 100 ids returns `400 Too many IDs requested (maximum is 100)`.
- Sending neither list returns `400 message_ids or whatsapp_message_ids is required`.
- Authentication is the same API key used to send. You can only see messages belonging to your own organization; anything else is reported as not found.
- A single unknown id on the one-message endpoint returns `404 Message not found`.

### Which one to use

- **Status callback** — use as the normal path. You are told as soon as something changes, with no polling.
- **Status lookup** — use as the safety net. Ask about anything you did not hear about, at whatever interval suits you. Once a day for unresolved records is usually plenty.

Do not poll every message on a timer. Callbacks already cover the normal case, and polling thousands of messages adds load for no benefit.

### Multipart requests

When sending `multipart/form-data` (a template with an uploaded header file), send both values as ordinary text form fields:

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -F "phone_number=919876543210" \
  -F "phone_number_id=116800811516809" \
  -F "template_name=credentials_document_v1" \
  -F "status_callback_url=https://billing.example.com/api/whatsapp-status" \
  -F "status_callback_bearer_token=your-secret-token" \
  -F "header_file=@/path/to/credentials.pdf;type=application/pdf"
```

## Invoice Approval Templates with Confirm / Reject Buttons

Use this when an external system needs to send an invoice approval message and receive the customer's button response back through an API call.

This is a different callback from the delivery status callback above. The status callback tells you what happened to the message; the response callback tells you what the customer answered. You can use either, both, or neither, and they may point at different URLs.

### What must exist first

Create and approve a WhatsApp template in Meta with quick reply buttons, for example:

- `Confirm`
- `Reject`

The customer will only see the button text. The invoice code can be sent as a hidden button payload.

### Send request

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "invoice_approval_v1",
    "template_params": {
      "customer_name": "Nitin Jain",
      "invoice_amount": "5000"
    },
    "button_payloads": {
      "0": "invoice_code=INV-1001&action=confirm",
      "1": "invoice_code=INV-1001&action=reject"
    },
    "response_callback_url": "https://billing.example.com/api/invoice-response",
    "response_callback_bearer_token": "your-secret-token",
    "status_callback_url": "https://billing.example.com/api/whatsapp-status",
    "status_callback_bearer_token": "your-secret-token"
  }'
```

Notes:

- `button_payloads` keys are WhatsApp button positions: `0` for the first button, `1` for the second button, `2` for the third button.
- You may also use the button text as the key, for example `"Confirm": "invoice_code=INV-1001&action=confirm"`.
- `response_callback_url` is called by Whatomate after the customer taps a button.
- `response_callback_bearer_token` is sent as `Authorization: Bearer <token>`.

### Callback sent by Whatomate

When the customer taps a button, Whatomate sends a POST request to `response_callback_url`.

Example:

```json
{
  "invoice_code": "INV-1001",
  "action": "confirm",
  "button_payload": "invoice_code=INV-1001&action=confirm",
  "button_id": "0",
  "button_text": "Confirm",
  "contact_id": "4c0a7f45-7d2f-4a7f-9a3c-1b0f6c4e5e20",
  "contact_phone": "919876543210",
  "contact_name": "Nitin Jain",
  "whatsapp_account": "Main Account",
  "incoming_message_id": "6d2b7e17-42a1-4f54-9b7a-2abf1b283a11",
  "incoming_whatsapp_message_id": "wamid.incoming",
  "original_message_id": "12415f80-1d08-4c17-9030-08302bdf37a6",
  "original_whatsapp_message_id": "wamid.original",
  "timestamp": "2026-06-09T10:30:00Z"
}
```

### Callback behavior

- The incoming WhatsApp message is still saved even if the callback API fails.
- Whatomate stores callback status in the incoming message metadata for troubleshooting.
- Callback failures are not retried automatically in this version.
- Keep `response_callback_bearer_token` secret and rotate it if exposed.

### 2. With header media URL

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "credentials_v1",
    "template_params": {
      "name": "Nitin Jain",
      "portal": "Income Tax"
    },
    "header_media_url": "https://cdn.example.com/template-header.jpg"
  }'
```

### 3. With document header name

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "credentials_document_v1",
    "template_params": {
      "name": "Nitin Jain",
      "portal": "Income Tax"
    },
    "header_media_url": "https://cdn.example.com/files/generated-download?id=abc123",
    "header_media_filename": "income-tax-credentials.pdf"
  }'
```

### 4. Multipart document upload with document name

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_api_key" \
  -F "phone_number=919876543210" \
  -F "phone_number_id=116800811516809" \
  -F "template_name=credentials_document_v1" \
  -F 'template_params={"name":"Nitin Jain","portal":"Income Tax"}' \
  -F "header_media_filename=income-tax-credentials.pdf" \
  -F "header_file=@/path/to/credentials.pdf;type=application/pdf"
```

### 5. Super admin API key for another organization

```bash
curl -X POST "https://your-whatomate-server/api/messages/template" \
  -H "X-API-Key: whm_your_super_admin_key" \
  -H "X-Organization-ID: 7d35e1f1-7b90-4b6c-8e70-6ef6a0e1c123" \
  -H "Content-Type: application/json" \
  -d '{
    "phone_number": "919876543210",
    "phone_number_id": "116800811516809",
    "template_name": "credentials_v1",
    "template_params": {
      "name": "Nitin Jain",
      "portal": "Income Tax"
    }
  }'
```

## Success Response

Typical response:

```json
{
  "status": "success",
  "data": {
    "id": "uuid",
    "contact_id": "uuid",
    "direction": "outgoing",
    "message_type": "template",
    "content": {
      "body": "Hello Nitin Jain! Your portal is Income Tax."
    },
    "interactive_data": null,
    "status": "pending",
    "is_reply": false,
    "whatsapp_account": "primary",
    "created_at": "2026-03-27T10:30:00Z",
    "updated_at": "2026-03-27T10:30:00Z"
  }
}
```

`"status": "pending"` means the request was accepted and queued — it does **not** mean the message has been sent. Use `status_callback_url` if you need to know the real outcome. Store the returned `id`; it is the `message_id` in every status callback.

## Error Cases

Common failures:

- `400 Either contact_id or phone_number is required`
- `400 Either template_name or template_id is required`
- `400 Invalid contact_id`
- `400 Invalid template_id`
- `400 Missing template parameters: ...`
- `400 WhatsApp account not found for phone_number_id`
- `400 WhatsApp account not found`
- `400 status_callback_url must start with http:// or https://`
- `400 status_callback_url must include a host`
- `400 status_callback_url is not a valid URL`
- `404 Template not found`
- `404 Contact not found`
- `401 Unauthorized`
- `403 Insufficient permissions`

These errors are returned on the original request. Because the message was never created, no status callback is sent for them.

## Operational Notes

- this endpoint sends the message through WhatsApp
- if `phone_number` is new, Whatomate creates the contact before sending
- prefer `phone_number_id` for external systems so account routing is explicit
- use `template_name` unless your integration already stores local template UUIDs
- the API response confirms acceptance only; use `status_callback_url` to learn whether the message was actually sent, delivered, read, or failed
