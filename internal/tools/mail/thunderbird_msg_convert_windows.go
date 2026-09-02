//go:build windows

package mail

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

const supercliRFC822HeadersSchema = "http://schemas.microsoft.com/mapi/string/{00020329-0000-0000-C000-000000000046}/SuperCLI-RFC822-Headers"

type msgConversionMeta struct {
	Subject             string `json:"subject,omitempty"`
	Date                string `json:"date,omitempty"`
	AttachmentCount     int    `json:"attachmentCount"`
	UsedOriginalHeaders bool   `json:"usedOriginalHeaders"`
	PreservedHTML       bool   `json:"preservedHtml"`
	PreservedMessageID  bool   `json:"preservedMessageId"`
	Converter           string `json:"converter"`
}

type msgConvertedAttachment struct {
	Name        string
	Path        string
	ContentType string
	ContentID   string
}

func saneMSGTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	now := time.Now()
	if value.Before(time.Date(1970, 1, 1, 0, 0, 0, 0, time.Local)) || value.After(now.Add(7*24*time.Hour)) {
		return time.Time{}
	}
	return value
}

func convertOutlookMSGToEML(ctx context.Context, source, destination string) (meta msgConversionMeta, err error) {
	meta.Converter = "outlook_com"
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if e := ole.CoInitialize(0); e != nil {
		if oleErr, ok := e.(*ole.OleError); !ok || oleErr.Code() != 1 { // S_FALSE
			return meta, fmt.Errorf("COM init: %w", e)
		}
	}
	defer ole.CoUninitialize()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Outlook COM panic while opening .msg: %v", r)
		}
	}()
	select {
	case <-ctx.Done():
		return meta, ctx.Err()
	default:
	}

	app, err := outlookApp()
	if err != nil {
		return meta, err
	}
	defer app.Release()

	nsRaw, err := oleutil.CallMethod(app, "GetNamespace", "MAPI")
	if err != nil {
		return meta, fmt.Errorf("GetNamespace(MAPI): %w", err)
	}
	ns := nsRaw.ToIDispatch()
	defer ns.Release()

	itemRaw, err := oleutil.CallMethod(ns, "OpenSharedItem", source)
	if err != nil {
		return meta, fmt.Errorf("Outlook cannot open .msg %q: %w", source, err)
	}
	item := itemRaw.ToIDispatch()
	if item == nil {
		return meta, fmt.Errorf("Outlook returned no message object for %q", source)
	}
	defer item.Release()

	transportRaw := firstMAPIString(item,
		supercliRFC822HeadersSchema,
		"http://schemas.microsoft.com/mapi/proptag/0x007D001F",
		"http://schemas.microsoft.com/mapi/proptag/0x007D001E",
	)
	originalHeaders := parseMSGTransportHeaders(transportRaw)
	meta.UsedOriginalHeaders = len(originalHeaders) > 0

	subject := cleanHeaderValue(originalHeaders.Get("Subject"))
	if subject == "" {
		subject = strings.TrimSpace(prop(item, "Subject"))
		if subject != "" {
			subject = mime.QEncoding.Encode("UTF-8", subject)
		}
	}
	meta.Subject = strings.TrimSpace(prop(item, "Subject"))

	from := cleanHeaderValue(originalHeaders.Get("From"))
	if from == "" {
		name := strings.TrimSpace(prop(item, "SenderName"))
		address := firstMAPIString(item,
			"http://schemas.microsoft.com/mapi/proptag/0x5D01001F",
			"http://schemas.microsoft.com/mapi/proptag/0x5D01001E",
		)
		if address == "" {
			address = strings.TrimSpace(prop(item, "SenderEmailAddress"))
		}
		from = formatMSGMailbox(name, address)
	}

	to := cleanHeaderValue(originalHeaders.Get("To"))
	if to == "" {
		to = cleanHeaderValue(prop(item, "To"))
	}
	cc := cleanHeaderValue(originalHeaders.Get("Cc"))
	if cc == "" {
		cc = cleanHeaderValue(prop(item, "CC"))
	}
	bcc := cleanHeaderValue(originalHeaders.Get("Bcc"))
	if bcc == "" {
		bcc = cleanHeaderValue(prop(item, "BCC"))
	}
	replyTo := cleanHeaderValue(originalHeaders.Get("Reply-To"))
	inReplyTo := cleanHeaderValue(originalHeaders.Get("In-Reply-To"))
	references := cleanHeaderValue(originalHeaders.Get("References"))

	dateValue := cleanHeaderValue(originalHeaders.Get("Date"))
	if dateValue == "" {
		when := saneMSGTime(propTime(item, "SentOn"))
		if when.IsZero() {
			when = saneMSGTime(propTime(item, "ReceivedTime"))
		}
		if when.IsZero() {
			when = saneMSGTime(propTime(item, "CreationTime"))
		}
		if when.IsZero() {
			when = saneMSGTime(propTime(item, "LastModificationTime"))
		}
		if when.IsZero() {
			if st, statErr := os.Stat(source); statErr == nil {
				when = saneMSGTime(st.ModTime())
			}
		}
		if when.IsZero() {
			when = time.Now()
		}
		dateValue = when.Format(time.RFC1123Z)
	}
	meta.Date = dateValue

	messageID := cleanHeaderValue(originalHeaders.Get("Message-Id"))
	if messageID == "" {
		messageID = cleanHeaderValue(firstMAPIString(item,
			"http://schemas.microsoft.com/mapi/proptag/0x1035001F",
			"http://schemas.microsoft.com/mapi/proptag/0x1035001E",
		))
	}
	if messageID != "" {
		meta.PreservedMessageID = true
		if !strings.HasPrefix(messageID, "<") {
			messageID = "<" + strings.Trim(messageID, "<>") + ">"
		}
	} else {
		messageID = deterministicMSGMessageID(source)
	}

	plainBody := prop(item, "Body")
	htmlBody := prop(item, "HTMLBody")
	meta.PreservedHTML = strings.TrimSpace(htmlBody) != ""
	body := plainBody
	bodyType := "text/plain; charset=UTF-8"
	if strings.TrimSpace(htmlBody) != "" {
		body = htmlBody
		bodyType = "text/html; charset=UTF-8"
	}

	tempDir, err := os.MkdirTemp("", "supercli-msg-attachments-*")
	if err != nil {
		return meta, fmt.Errorf("create attachment temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	attachments, err := extractMSGAttachments(ctx, item, tempDir)
	if err != nil {
		return meta, err
	}
	meta.AttachmentCount = len(attachments)

	out, err := os.Create(destination)
	if err != nil {
		return meta, fmt.Errorf("create .eml: %w", err)
	}
	defer func() {
		if closeErr := out.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	writeHeaderLine(out, "From", from)
	writeHeaderLine(out, "To", to)
	writeHeaderLine(out, "Cc", cc)
	writeHeaderLine(out, "Bcc", bcc)
	writeHeaderLine(out, "Reply-To", replyTo)
	writeHeaderLine(out, "Date", dateValue)
	writeHeaderLine(out, "Subject", subject)
	writeHeaderLine(out, "Message-ID", messageID)
	writeHeaderLine(out, "In-Reply-To", inReplyTo)
	writeHeaderLine(out, "References", references)
	writeHeaderLine(out, "MIME-Version", "1.0")

	if len(attachments) == 0 {
		writeHeaderLine(out, "Content-Type", bodyType)
		writeHeaderLine(out, "Content-Transfer-Encoding", "quoted-printable")
		if _, err = io.WriteString(out, "\r\n"); err != nil {
			return meta, err
		}
		qp := quotedprintable.NewWriter(out)
		if _, err = io.WriteString(qp, body); err != nil {
			_ = qp.Close()
			return meta, err
		}
		if err = qp.Close(); err != nil {
			return meta, err
		}
		return meta, nil
	}

	mixed := multipart.NewWriter(out)
	writeHeaderLine(out, "Content-Type", fmt.Sprintf("multipart/mixed; boundary=%q", mixed.Boundary()))
	if _, err = io.WriteString(out, "\r\n"); err != nil {
		return meta, err
	}

	bodyHeader := make(textproto.MIMEHeader)
	bodyHeader.Set("Content-Type", bodyType)
	bodyHeader.Set("Content-Transfer-Encoding", "quoted-printable")
	bodyPart, err := mixed.CreatePart(bodyHeader)
	if err != nil {
		return meta, fmt.Errorf("create body MIME part: %w", err)
	}
	qp := quotedprintable.NewWriter(bodyPart)
	if _, err = io.WriteString(qp, body); err != nil {
		_ = qp.Close()
		return meta, err
	}
	if err = qp.Close(); err != nil {
		return meta, err
	}

	for _, attachment := range attachments {
		select {
		case <-ctx.Done():
			return meta, ctx.Err()
		default:
		}
		h := make(textproto.MIMEHeader)
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		h.Set("Content-Type", mime.FormatMediaType(contentType, map[string]string{"name": attachment.Name}))
		disposition := "attachment"
		if attachment.ContentID != "" {
			disposition = "inline"
			h.Set("Content-ID", "<"+strings.Trim(attachment.ContentID, "<>")+">")
		}
		h.Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Name}))
		h.Set("Content-Transfer-Encoding", "base64")
		part, partErr := mixed.CreatePart(h)
		if partErr != nil {
			return meta, fmt.Errorf("create attachment MIME part: %w", partErr)
		}
		if partErr = writeBase64File(part, attachment.Path); partErr != nil {
			return meta, fmt.Errorf("encode attachment %q: %w", attachment.Name, partErr)
		}
	}
	if err = mixed.Close(); err != nil {
		return meta, fmt.Errorf("close MIME message: %w", err)
	}
	return meta, nil
}

func firstMAPIString(dispatch *ole.IDispatch, schemas ...string) string {
	paRaw, err := oleutil.GetProperty(dispatch, "PropertyAccessor")
	if err != nil {
		return ""
	}
	pa := paRaw.ToIDispatch()
	if pa == nil {
		return ""
	}
	defer pa.Release()
	for _, schema := range schemas {
		value, callErr := oleutil.CallMethod(pa, "GetProperty", schema)
		if callErr != nil || value == nil {
			continue
		}
		raw := strings.TrimSpace(fmt.Sprintf("%v", value.Value()))
		value.Clear()
		if raw != "" && raw != "<nil>" {
			return raw
		}
	}
	return ""
}

func parseMSGTransportHeaders(raw string) mail.Header {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	msg, err := mail.ReadMessage(strings.NewReader(raw + "\n\n"))
	if err != nil {
		return nil
	}
	return msg.Header
}

func cleanHeaderValue(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

func formatMSGMailbox(name, address string) string {
	name = cleanHeaderValue(name)
	address = cleanHeaderValue(address)
	if address == "" {
		return name
	}
	if strings.Contains(address, "@") {
		return (&mail.Address{Name: name, Address: address}).String()
	}
	if name != "" {
		return name + " <" + address + ">"
	}
	return address
}

func deterministicMSGMessageID(source string) string {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Sprintf("<supercli-msg-%d@local>", time.Now().UnixNano())
	}
	defer file.Close()
	h := sha256.New()
	_, _ = io.Copy(h, file)
	digest := hex.EncodeToString(h.Sum(nil))
	if len(digest) > 32 {
		digest = digest[:32]
	}
	return "<supercli-msg-" + digest + "@local>"
}

func extractMSGAttachments(ctx context.Context, item *ole.IDispatch, tempDir string) ([]msgConvertedAttachment, error) {
	attachmentsRaw, err := oleutil.GetProperty(item, "Attachments")
	if err != nil {
		return nil, nil
	}
	attachments := attachmentsRaw.ToIDispatch()
	if attachments == nil {
		return nil, nil
	}
	defer attachments.Release()
	countRaw, err := oleutil.GetProperty(attachments, "Count")
	if err != nil {
		return nil, fmt.Errorf("read .msg attachment count: %w", err)
	}
	count := int(toInt64(countRaw.Value()))
	countRaw.Clear()
	result := make([]msgConvertedAttachment, 0, count)
	for i := 1; i <= count; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		attRaw, getErr := oleutil.GetProperty(attachments, "Item", i)
		if getErr != nil {
			return nil, fmt.Errorf("read .msg attachment %d: %w", i, getErr)
		}
		att := attRaw.ToIDispatch()
		if att == nil {
			continue
		}
		name := sanitizeMSGAttachmentName(prop(att, "FileName"), i)
		// The original display name can legitimately be very long (for example
		// percent-encoded exports). Outlook SaveAsFile still uses legacy Windows
		// path limits, so keep the MIME name but use a short deterministic name
		// only for the temporary on-disk copy.
		path := filepath.Join(tempDir, msgAttachmentTempName(name, i))
		_, saveErr := oleutil.CallMethod(att, "SaveAsFile", path)
		contentID := firstMAPIString(att,
			"http://schemas.microsoft.com/mapi/proptag/0x3712001F",
			"http://schemas.microsoft.com/mapi/proptag/0x3712001E",
		)
		contentType := firstMAPIString(att,
			"http://schemas.microsoft.com/mapi/proptag/0x370E001F",
			"http://schemas.microsoft.com/mapi/proptag/0x370E001E",
		)
		att.Release()
		if saveErr != nil {
			return nil, fmt.Errorf("save .msg attachment %q: %w", name, saveErr)
		}
		if contentType == "" {
			contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		}
		if contentType == "" {
			if file, openErr := os.Open(path); openErr == nil {
				buf := make([]byte, 512)
				n, _ := file.Read(buf)
				_ = file.Close()
				if n > 0 {
					contentType = http.DetectContentType(buf[:n])
				}
			}
		}
		result = append(result, msgConvertedAttachment{
			Name:        name,
			Path:        path,
			ContentType: contentType,
			ContentID:   cleanHeaderValue(contentID),
		})
	}
	return result, nil
}

func sanitizeMSGAttachmentName(name string, index int) string {
	name = strings.TrimSpace(filepath.Base(name))
	if name == "" || name == "." {
		name = fmt.Sprintf("attachment-%d.bin", index)
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*':
			return '_'
		}
		if r < 32 {
			return '_'
		}
		return r
	}, name)
	return name
}

func msgAttachmentTempName(name string, index int) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 16 || strings.ContainsAny(ext, `<>:"/\|?*`) {
		ext = ""
	}
	digest := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%03d-%s%s", index, hex.EncodeToString(digest[:6]), ext)
}

func writeHeaderLine(w io.Writer, key, value string) {
	value = cleanHeaderValue(value)
	if value == "" {
		return
	}
	_, _ = fmt.Fprintf(w, "%s: %s\r\n", key, value)
}

func writeBase64File(w io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	buf := make([]byte, 57) // 57 source bytes -> exactly 76 base64 chars.
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(buf)))
	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			out := encoded[:base64.StdEncoding.EncodedLen(n)]
			base64.StdEncoding.Encode(out, buf[:n])
			if _, err := w.Write(out); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "\r\n"); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}
