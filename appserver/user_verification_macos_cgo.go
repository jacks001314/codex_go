//go:build darwin && cgo

package appserver

/*
#cgo CFLAGS: -fblocks -x objective-c
#cgo LDFLAGS: -framework Security -framework LocalAuthentication -framework CoreFoundation -framework Foundation
#include <Security/Security.h>
#include <LocalAuthentication/LocalAuthentication.h>
#include <CoreFoundation/CoreFoundation.h>
#include <objc/runtime.h>
#include <objc/message.h>
#include <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>

typedef struct { int domain; long code; } uv_error;

enum {
    UV_DOMAIN_NONE = 0,
    UV_DOMAIN_LOCAL_AUTHENTICATION = 1,
    UV_DOMAIN_NSOSSTATUS = 2,
};

enum {
    UV_OK = 0,
    UV_ERROR = 1,
    UV_INVALID_PROTECTION = 2,
    UV_CANCELLED = 3,
    UV_MISSING = 4,
};

static void uv_clear_error(uv_error *out) {
    if (out) { out->domain = UV_DOMAIN_NONE; out->code = 0; }
}

static int uv_domain_code(NSString *domain) {
    const char *name = [domain UTF8String];
    if (name && strcmp(name, "com.apple.LocalAuthentication") == 0) {
        return UV_DOMAIN_LOCAL_AUTHENTICATION;
    }
    return UV_DOMAIN_NSOSSTATUS;
}

static void uv_error_from_nserror(NSError *error, uv_error *out) {
    if (!out) { return; }
    if (!error) { out->domain = UV_DOMAIN_NSOSSTATUS; out->code = 0; return; }
    out->domain = uv_domain_code([error domain]);
    out->code = (long)[error code];
}

static void uv_error_from_cferror(CFErrorRef error, uv_error *out) {
    if (!out) { return; }
    if (!error) { out->domain = UV_DOMAIN_NSOSSTATUS; out->code = 0; return; }
    CFStringRef domain = CFErrorGetDomain(error);
    char buffer[128];
    int local = 0;
    if (domain && CFStringGetCString(domain, buffer, sizeof(buffer), kCFStringEncodingUTF8)) {
        local = strcmp(buffer, "com.apple.LocalAuthentication") == 0;
    }
    out->domain = local ? UV_DOMAIN_LOCAL_AUTHENTICATION : UV_DOMAIN_NSOSSTATUS;
    out->code = (long)CFErrorGetCode(error);
}

static void uv_set_osstatus(OSStatus status, uv_error *out) {
    if (!out) { return; }
    out->domain = UV_DOMAIN_NSOSSTATUS;
    out->code = (long)status;
}

static id uv_context(int interaction_allowed) {
    Class cls = objc_getClass("LAContext");
    if (!cls) { return nil; }
    id ctx = ((id(*)(id,SEL))objc_msgSend)((id)cls, sel_registerName("alloc"));
    ctx = ((id(*)(id,SEL))objc_msgSend)(ctx, sel_registerName("init"));
    ((void(*)(id,SEL,BOOL))objc_msgSend)(ctx, sel_registerName("setInteractionNotAllowed:"), interaction_allowed ? NO : YES);
    return ctx;
}

static void uv_invalidate(id ctx) {
    if (ctx) {
        ((void(*)(id,SEL))objc_msgSend)(ctx, sel_registerName("invalidate"));
    }
}

static int uv_can_evaluate(id ctx, uv_error *out) {
    NSError *error = nil;
    BOOL ok = ((BOOL(*)(id,SEL,NSInteger,NSError**))objc_msgSend)(
        ctx, sel_registerName("canEvaluatePolicy:error:"),
        (NSInteger)LAPolicyDeviceOwnerAuthenticationWithBiometrics, &error);
    if (ok) { return 1; }
    uv_error_from_nserror(error, out);
    return 0;
}

static int uv_validate_key(SecKeyRef key) {
    if (!key) { return 0; }
    CFDictionaryRef attributes = SecKeyCopyAttributes(key);
    if (!attributes) { return 0; }
    int valid = 0;
    CFTypeRef token = CFDictionaryGetValue(attributes, kSecAttrTokenID);
    CFTypeRef key_class = CFDictionaryGetValue(attributes, kSecAttrKeyClass);
    CFTypeRef key_type = CFDictionaryGetValue(attributes, kSecAttrKeyType);
    CFTypeRef key_size = CFDictionaryGetValue(attributes, kSecAttrKeySizeInBits);
    int bits = 0;
    if (key_size && CFGetTypeID(key_size) == CFNumberGetTypeID()) {
        CFNumberGetValue((CFNumberRef)key_size, kCFNumberIntType, &bits);
    }
    if (token && key_class && key_type && bits == 256 &&
        CFEqual(token, kSecAttrTokenIDSecureEnclave) &&
        CFEqual(key_class, kSecAttrKeyClassPrivate) &&
        CFEqual(key_type, kSecAttrKeyTypeECSECPrimeRandom)) {
        valid = 1;
    }
    CFRelease(attributes);
    return valid;
}

// uv_export_public_key returns the SEC1 uncompressed point (0x04 || X || Y). The
// SubjectPublicKeyInfo wrapper is built in Go with crypto/x509 so it cannot
// drift from the DER Rust's p256 crate emits.
static int uv_export_public_key(SecKeyRef key, unsigned char **der_out, int *len_out, uv_error *out) {
    SecKeyRef public_key = SecKeyCopyPublicKey(key);
    if (!public_key) {
        uv_set_osstatus(errSecParam, out);
        return UV_ERROR;
    }
    CFErrorRef error = NULL;
    CFDataRef data = SecKeyCopyExternalRepresentation(public_key, &error);
    CFRelease(public_key);
    if (!data) {
        uv_error_from_cferror(error, out);
        if (error) { CFRelease(error); }
        return UV_ERROR;
    }
    CFIndex length = CFDataGetLength(data);
    const UInt8 *bytes = CFDataGetBytePtr(data);
    if (length != 65 || !bytes || bytes[0] != 0x04) {
        CFRelease(data);
        uv_set_osstatus(errSecParam, out);
        return UV_ERROR;
    }
    unsigned char *buffer = malloc(65);
    if (!buffer) {
        CFRelease(data);
        uv_set_osstatus(errSecAllocate, out);
        return UV_ERROR;
    }
    memcpy(buffer, bytes, 65);
    CFRelease(data);
    *der_out = buffer;
    *len_out = 65;
    return UV_OK;
}

// uv_test_export_ephemeral_sec1 exports a throwaway software P-256 key through
// the same SecKeyCopyExternalRepresentation call the provider uses for
// credentials. Tests compare the result with the pure-Go SPKI derivation; it
// needs no Secure Enclave, which keeps that parity check runnable on any Mac.
static int uv_test_export_ephemeral_sec1(unsigned char **out, int *len_out, uv_error *err) {
    *out = NULL;
    *len_out = 0;
    int bits = 256;
    CFNumberRef size = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &bits);
    CFMutableDictionaryRef attrs = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (!size || !attrs) {
        if (size) { CFRelease(size); }
        if (attrs) { CFRelease(attrs); }
        uv_set_osstatus(errSecAllocate, err);
        return UV_ERROR;
    }
    CFDictionarySetValue(attrs, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
    CFDictionarySetValue(attrs, kSecAttrKeySizeInBits, size);
    CFDictionarySetValue(attrs, kSecAttrIsPermanent, kCFBooleanFalse);
    CFErrorRef error = NULL;
    SecKeyRef key = SecKeyCreateRandomKey(attrs, &error);
    CFRelease(attrs);
    CFRelease(size);
    if (!key) {
        uv_error_from_cferror(error, err);
        if (error) { CFRelease(error); }
        return UV_ERROR;
    }
    SecKeyRef public_key = SecKeyCopyPublicKey(key);
    CFRelease(key);
    if (!public_key) {
        uv_set_osstatus(errSecParam, err);
        return UV_ERROR;
    }
    CFDataRef data = SecKeyCopyExternalRepresentation(public_key, &error);
    CFRelease(public_key);
    if (!data) {
        uv_error_from_cferror(error, err);
        if (error) { CFRelease(error); }
        return UV_ERROR;
    }
    CFIndex length = CFDataGetLength(data);
    const UInt8 *bytes = CFDataGetBytePtr(data);
    unsigned char *buffer = NULL;
    if (bytes && length > 0) {
        buffer = malloc((size_t)length);
        if (buffer) { memcpy(buffer, bytes, (size_t)length); }
    }
    CFRelease(data);
    if (!buffer) {
        uv_set_osstatus(errSecAllocate, err);
        return UV_ERROR;
    }
    *out = buffer;
    *len_out = (int)length;
    return UV_OK;
}

static OSStatus uv_copy_key(const char *label, id ctx, int want_attributes, CFTypeRef *result) {
    CFStringRef label_string = CFStringCreateWithCString(kCFAllocatorDefault, label, kCFStringEncodingUTF8);
    if (!label_string) { return errSecParam; }
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (!query) {
        CFRelease(label_string);
        return errSecAllocate;
    }
    CFDictionarySetValue(query, kSecClass, kSecClassKey);
    CFDictionarySetValue(query, kSecAttrKeyClass, kSecAttrKeyClassPrivate);
    CFDictionarySetValue(query, kSecAttrApplicationLabel, label_string);
    CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecReturnRef, kCFBooleanTrue);
    if (want_attributes) {
        CFDictionarySetValue(query, kSecReturnAttributes, kCFBooleanTrue);
    }
    if (ctx) {
        CFDictionarySetValue(query, kSecUseAuthenticationContext, ctx);
    }
    OSStatus status = SecItemCopyMatching(query, result);
    CFRelease(query);
    CFRelease(label_string);
    return status;
}

static int uv_find_key(const char *label, int *has_key, unsigned char **der_out, int *len_out, uv_error *out) {
    *has_key = 0;
    *der_out = NULL;
    *len_out = 0;
    id ctx = uv_context(0);
    CFTypeRef result = NULL;
    OSStatus status = uv_copy_key(label, ctx, 0, &result);
    uv_invalidate(ctx);
    if (status == errSecItemNotFound) { return UV_OK; }
    if (status != errSecSuccess || !result) {
        uv_set_osstatus(status, out);
        return UV_ERROR;
    }
    SecKeyRef key = (SecKeyRef)result;
    if (!uv_validate_key(key)) {
        CFRelease(result);
        return UV_INVALID_PROTECTION;
    }
    int rc = uv_export_public_key(key, der_out, len_out, out);
    CFRelease(result);
    if (rc == UV_OK) { *has_key = 1; }
    return rc;
}

static int uv_create_key(const char *label, unsigned char **der_out, int *len_out, uv_error *out) {
    CFStringRef label_string = CFStringCreateWithCString(kCFAllocatorDefault, label, kCFStringEncodingUTF8);
    if (!label_string) {
        uv_set_osstatus(errSecParam, out);
        return UV_ERROR;
    }
    CFErrorRef access_error = NULL;
    SecAccessControlRef access = SecAccessControlCreateWithFlags(
        kCFAllocatorDefault,
        kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
        kSecAccessControlBiometryAny | kSecAccessControlPrivateKeyUsage,
        &access_error);
    if (!access) {
        uv_error_from_cferror(access_error, out);
        if (access_error) { CFRelease(access_error); }
        CFRelease(label_string);
        return UV_ERROR;
    }
    int      bits = 256;
    CFNumberRef size = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &bits);
    CFMutableDictionaryRef private_attributes = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(private_attributes, kSecAttrIsPermanent, kCFBooleanTrue);
    CFDictionarySetValue(private_attributes, kSecAttrApplicationLabel, label_string);
    CFDictionarySetValue(private_attributes, kSecAttrAccessControl, access);
    CFMutableDictionaryRef attributes = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(attributes, kSecAttrKeyType, kSecAttrKeyTypeECSECPrimeRandom);
    CFDictionarySetValue(attributes, kSecAttrKeySizeInBits, size);
    CFDictionarySetValue(attributes, kSecAttrTokenID, kSecAttrTokenIDSecureEnclave);
    CFDictionarySetValue(attributes, kSecUseDataProtectionKeychain, kCFBooleanTrue);
    CFDictionarySetValue(attributes, kSecPrivateKeyAttrs, private_attributes);
    CFErrorRef error = NULL;
    SecKeyRef key = SecKeyCreateRandomKey(attributes, &error);
    CFRelease(attributes);
    CFRelease(private_attributes);
    CFRelease(size);
    CFRelease(access);
    CFRelease(label_string);
    if (!key) {
        uv_error_from_cferror(error, out);
        if (error) { CFRelease(error); }
        return UV_ERROR;
    }
    if (!uv_validate_key(key)) {
        CFRelease(key);
        return UV_INVALID_PROTECTION;
    }
    int rc = uv_export_public_key(key, der_out, len_out, out);
    CFRelease(key);
    return rc;
}

static int uv_delete_keys(const char *label, uv_error *out) {
    CFStringRef label_string = CFStringCreateWithCString(kCFAllocatorDefault, label, kCFStringEncodingUTF8);
    if (!label_string) {
        uv_set_osstatus(errSecParam, out);
        return UV_ERROR;
    }
    CFMutableDictionaryRef query = CFDictionaryCreateMutable(
        kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    CFDictionarySetValue(query, kSecClass, kSecClassKey);
    CFDictionarySetValue(query, kSecAttrApplicationLabel, label_string);
    CFDictionarySetValue(query, kSecUseDataProtectionKeychain, kCFBooleanTrue);
    OSStatus status = SecItemDelete(query);
    CFRelease(query);
    CFRelease(label_string);
    if (status == errSecSuccess || status == errSecItemNotFound) { return UV_OK; }
    uv_set_osstatus(status, out);
    return UV_ERROR;
}

static int uv_device_supported(void) {
    id ctx = uv_context(0);
    if (!ctx) { return 0; }
    uv_error ignored;
    uv_clear_error(&ignored);
    // canEvaluatePolicy populates biometryType even when enrollment or a
    // temporary lockout prevents authentication.
    uv_can_evaluate(ctx, &ignored);
    long biometry = (long)((NSInteger(*)(id,SEL))objc_msgSend)(ctx, sel_registerName("biometryType"));
    uv_invalidate(ctx);
    return biometry == 1; // kLABiometryTypeTouchID
}

static int uv_biometrics_available(uv_error *out) {
    id ctx = uv_context(0);
    if (!ctx) {
        uv_set_osstatus(errSecUnimplemented, out);
        return 0;
    }
    int ok = uv_can_evaluate(ctx, out);
    uv_invalidate(ctx);
    return ok;
}

static SecAccessControlRef uv_access_control_for_label(const char *label, uv_error *out) {
    id lookup = uv_context(0);
    CFTypeRef result = NULL;
    OSStatus status = uv_copy_key(label, lookup, 1, &result);
    uv_invalidate(lookup);
    if (status == errSecItemNotFound) {
        // SecItemCopyMatching leaves *result untouched on failure; releasing it
        // would abort the process in CoreFoundation. Report the status so the Go
        // side can answer credentialMissing like Rust's find/verify paths.
        uv_set_osstatus(errSecItemNotFound, out);
        return NULL;
    }
    if (status != errSecSuccess || !result || CFGetTypeID(result) != CFDictionaryGetTypeID()) {
        uv_set_osstatus(status, out);
        if (result) { CFRelease(result); }
        return NULL;
    }
    CFDictionaryRef attributes = (CFDictionaryRef)result;
    SecKeyRef key = NULL;
    CFTypeRef value_ref = CFDictionaryGetValue(attributes, kSecValueRef);
    if (value_ref) { key = (SecKeyRef)value_ref; }
    if (!uv_validate_key(key)) {
        CFRelease(result);
        return (SecAccessControlRef)errSecParam; // sentinel: invalid protection
    }
    CFTypeRef access = CFDictionaryGetValue(attributes, kSecAttrAccessControl);
    if (!access) {
        CFRelease(result);
        uv_set_osstatus(errSecParam, out);
        return NULL;
    }
    SecAccessControlRef retained = (SecAccessControlRef)CFRetain(access);
    CFRelease(result);
    return retained;
}

static int uv_verify(
    const char *label,
    const unsigned char *challenge,
    int challenge_len,
    const char *title,
    const char *description,
    volatile int *cancelled,
    unsigned char **signature_out,
    int *signature_len,
    unsigned char **der_out,
    int *der_len,
    uv_error *out
) {
    *signature_out = NULL;
    *signature_len = 0;
    *der_out = NULL;
    *der_len = 0;
    if (cancelled && *cancelled) { return UV_CANCELLED; }

    CFMutableStringRef reason = CFStringCreateMutable(kCFAllocatorDefault, 0);
    if (!reason) {
        uv_set_osstatus(errSecAllocate, out);
        return UV_ERROR;
    }
    CFStringAppendCString(reason, title, kCFStringEncodingUTF8);
    if (description && description[0] != '\0') {
        CFStringAppendCString(reason, "\n\n", kCFStringEncodingUTF8);
        CFStringAppendCString(reason, description, kCFStringEncodingUTF8);
    }

    id ctx = uv_context(0); // interaction allowed
    if (!ctx) {
        CFRelease(reason);
        uv_set_osstatus(errSecUnimplemented, out);
        return UV_ERROR;
    }
    ((void(*)(id,SEL,BOOL))objc_msgSend)(ctx, sel_registerName("setInteractionNotAllowed:"), NO);
    // Rust sets localized_fallback_title to the empty string, which removes the
    // "Use Password" button; nil would restore the default fallback UI.
    CFStringRef empty_fallback = CFStringCreateWithCString(kCFAllocatorDefault, "", kCFStringEncodingUTF8);
    if (empty_fallback) {
        ((void(*)(id,SEL,id))objc_msgSend)(ctx, sel_registerName("setLocalizedFallbackTitle:"), (id)empty_fallback);
        CFRelease(empty_fallback);
    }
    ((void(*)(id,SEL,id))objc_msgSend)(ctx, sel_registerName("setLocalizedReason:"), (id)reason);

    // Rust checks biometric readiness before it reads the key's attributes, so an
    // unavailable sensor is reported ahead of a key-protection mismatch.
    if (!uv_can_evaluate(ctx, out)) {
        uv_invalidate(ctx);
        CFRelease(reason);
        return UV_ERROR;
    }
    if (cancelled && *cancelled) {
        uv_invalidate(ctx);
        CFRelease(reason);
        return UV_CANCELLED;
    }

    SecAccessControlRef access = uv_access_control_for_label(label, out);
    if (!access) {
        uv_invalidate(ctx);
        CFRelease(reason);
        return (out && out->code == errSecItemNotFound) ? UV_MISSING : UV_ERROR;
    }
    if (access == (SecAccessControlRef)errSecParam) {
        uv_invalidate(ctx);
        CFRelease(reason);
        return UV_INVALID_PROTECTION;
    }

    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    __block int authorized = 0;
    __block NSError *authorization_error = nil;
    void (^reply)(BOOL, NSError *) = ^(BOOL success, NSError *error) {
        authorized = success ? 1 : 0;
        authorization_error = [error retain];
        dispatch_semaphore_signal(semaphore);
    };
    ((void(*)(id,SEL,SecAccessControlRef,NSInteger,id,void*))objc_msgSend)(
        ctx,
        sel_registerName("evaluateAccessControl:operation:localizedReason:reply:"),
        access,
        (NSInteger)LAAccessControlOperationUseKeySign,
        (id)reason,
        (void *)reply);

    int was_cancelled = 0;
    while (dispatch_semaphore_wait(semaphore, dispatch_time(DISPATCH_TIME_NOW, 50 * NSEC_PER_MSEC)) != 0) {
        if (cancelled && *cancelled && !was_cancelled) {
            was_cancelled = 1;
            uv_invalidate(ctx);
        }
    }
    if (cancelled && *cancelled) { was_cancelled = 1; }

    CFRelease(reason);
    CFRelease(access);
    if (was_cancelled) {
        if (authorization_error) { [authorization_error release]; }
        uv_invalidate(ctx);
        return UV_CANCELLED;
    }
    if (!authorized) {
        uv_error_from_nserror(authorization_error, out);
        if (authorization_error) { [authorization_error release]; }
        uv_invalidate(ctx);
        return UV_ERROR;
    }
    if (authorization_error) { [authorization_error release]; }

    // Signing must fail rather than open another prompt: reuse the authorized
    // context with interaction disabled.
    ((void(*)(id,SEL,BOOL))objc_msgSend)(ctx, sel_registerName("setInteractionNotAllowed:"), YES);
    CFTypeRef result = NULL;
    OSStatus status = uv_copy_key(label, ctx, 0, &result);
    if (status == errSecItemNotFound) {
        uv_invalidate(ctx);
        return UV_MISSING;
    }
    if (status != errSecSuccess || !result) {
        uv_set_osstatus(status, out);
        uv_invalidate(ctx);
        return UV_ERROR;
    }
    SecKeyRef key = (SecKeyRef)result;
    if (!uv_validate_key(key)) {
        CFRelease(result);
        uv_invalidate(ctx);
        return UV_INVALID_PROTECTION;
    }
    if (uv_export_public_key(key, der_out, der_len, out) != UV_OK) {
        CFRelease(result);
        uv_invalidate(ctx);
        return UV_ERROR;
    }
    CFDataRef data = CFDataCreate(kCFAllocatorDefault, challenge, challenge_len);
    CFErrorRef sign_error = NULL;
    CFDataRef signature = SecKeyCreateSignature(
        key, kSecKeyAlgorithmECDSASignatureMessageX962SHA256, data, &sign_error);
    CFRelease(data);
    CFRelease(result);
    uv_invalidate(ctx);
    if (!signature) {
        uv_error_from_cferror(sign_error, out);
        if (sign_error) { CFRelease(sign_error); }
        return UV_ERROR;
    }
    CFIndex length = CFDataGetLength(signature);
    unsigned char *buffer = malloc((size_t)length);
    if (!buffer) {
        CFRelease(signature);
        uv_set_osstatus(errSecAllocate, out);
        return UV_ERROR;
    }
    memcpy(buffer, CFDataGetBytePtr(signature), (size_t)length);
    CFRelease(signature);
    *signature_out = buffer;
    *signature_len = (int)length;
    return UV_OK;
}
*/
import "C"

import (
	"sync/atomic"
	"time"
	"unsafe"
)

type cgoDarwinUserVerificationBackend struct{}

// newUserVerificationPlatformProvider selects the native Secure Enclave
// provider on a cgo-enabled macOS build.
func newUserVerificationPlatformProvider() UserVerificationProvider {
	return &darwinUserVerificationProvider{backend: cgoDarwinUserVerificationBackend{}}
}

// darwinUserVerificationTestSEC1 exports a throwaway software P-256 key's SEC1
// point through Security.framework. It exists so tests can pin the encoding the
// provider relies on without Secure Enclave hardware; production code never
// calls it.
func darwinUserVerificationTestSEC1() ([]byte, *darwinUserVerificationError) {
	var der *C.uchar
	var length C.int
	var native C.uv_error
	C.uv_clear_error(&native)
	if C.uv_test_export_ephemeral_sec1(&der, &length, &native) != C.UV_OK {
		return nil, classifyDarwinNativeError(native)
	}
	defer C.free(unsafe.Pointer(der))
	return C.GoBytes(unsafe.Pointer(der), length), nil
}

func (cgoDarwinUserVerificationBackend) DeviceSupported() bool {
	return C.uv_device_supported() == 1
}

func (cgoDarwinUserVerificationBackend) BiometricsAvailable() *darwinUserVerificationError {
	var native C.uv_error
	C.uv_clear_error(&native)
	if C.uv_biometrics_available(&native) == 1 {
		return nil
	}
	return classifyDarwinNativeError(native)
}

func (cgoDarwinUserVerificationBackend) FindKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError) {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	var has C.int
	var der *C.uchar
	var length C.int
	var native C.uv_error
	C.uv_clear_error(&native)
	switch C.uv_find_key(cLabel, &has, &der, &length, &native) {
	case C.UV_ERROR:
		return nil, classifyDarwinNativeError(native)
	case C.UV_INVALID_PROTECTION:
		return nil, darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, darwinUserVerificationInvalidProtectionMessage)
	case C.UV_OK:
		if has == 0 || der == nil || length <= 0 {
			return nil, nil
		}
		defer C.free(unsafe.Pointer(der))
		key, keyErr := darwinUserVerificationKeyFromSEC1(C.GoBytes(unsafe.Pointer(der), length))
		if keyErr != nil {
			return nil, keyErr
		}
		return key, nil
	default:
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, "the platform could not complete user verification")
	}
}

func (cgoDarwinUserVerificationBackend) CreateKey(label string) (*darwinUserVerificationKey, *darwinUserVerificationError) {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	var der *C.uchar
	var length C.int
	var native C.uv_error
	C.uv_clear_error(&native)
	switch C.uv_create_key(cLabel, &der, &length, &native) {
	case C.UV_ERROR:
		return nil, classifyDarwinNativeError(native)
	case C.UV_INVALID_PROTECTION:
		return nil, darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, darwinUserVerificationInvalidProtectionMessage)
	case C.UV_OK:
		defer C.free(unsafe.Pointer(der))
		key, keyErr := darwinUserVerificationKeyFromSEC1(C.GoBytes(unsafe.Pointer(der), length))
		if keyErr != nil {
			return nil, keyErr
		}
		return key, nil
	default:
		return nil, darwinUserVerificationFailed(UserVerificationFailureProviderError, "the platform could not complete user verification")
	}
}

func (cgoDarwinUserVerificationBackend) DeleteKeys(label string) *darwinUserVerificationError {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	var native C.uv_error
	C.uv_clear_error(&native)
	if C.uv_delete_keys(cLabel, &native) == C.UV_OK {
		return nil
	}
	return classifyDarwinNativeError(native)
}

func (cgoDarwinUserVerificationBackend) AuthorizeAndSign(label string, challenge []byte, title, description string, cancelled func() bool) ([]byte, string, *darwinUserVerificationError) {
	cLabel := C.CString(label)
	defer C.free(unsafe.Pointer(cLabel))
	cTitle := C.CString(title)
	defer C.free(unsafe.Pointer(cTitle))
	cDescription := C.CString(description)
	defer C.free(unsafe.Pointer(cDescription))
	var challengePointer *C.uchar
	if len(challenge) > 0 {
		challengePointer = (*C.uchar)(unsafe.Pointer(&challenge[0]))
	}
	var signature *C.uchar
	var signatureLength C.int
	var der *C.uchar
	var derLength C.int
	var native C.uv_error
	C.uv_clear_error(&native)

	// The C loop polls this flag every 50 ms while LocalAuthentication owns the
	// prompt, so cancelling invalidates the context that actually shows it.
	cancelFlag := (*C.int)(C.malloc(C.size_t(unsafe.Sizeof(C.int(0)))))
	if cancelFlag == nil {
		return nil, "", darwinUserVerificationFailed(UserVerificationFailureProviderError, "the platform could not complete user verification")
	}
	defer C.free(unsafe.Pointer(cancelFlag))
	*cancelFlag = 0
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(darwinUserVerificationLockPollTime)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if cancelled != nil && cancelled() {
					atomic.StoreInt32((*int32)(unsafe.Pointer(cancelFlag)), 1)
					return
				}
			}
		}
	}()
	status := C.uv_verify(
		cLabel,
		challengePointer,
		C.int(len(challenge)),
		cTitle,
		cDescription,
		cancelFlag,
		&signature,
		&signatureLength,
		&der,
		&derLength,
		&native,
	)
	close(stop)
	switch status {
	case C.UV_OK:
		if signature == nil || signatureLength <= 0 || der == nil || derLength <= 0 {
			if der != nil && derLength > 0 {
				C.free(unsafe.Pointer(der))
			}
			if signature != nil && signatureLength > 0 {
				C.free(unsafe.Pointer(signature))
			}
			return nil, "", darwinUserVerificationFailed(UserVerificationFailureProviderError, "the platform could not complete user verification")
		}
		key, keyErr := darwinUserVerificationKeyFromSEC1(C.GoBytes(unsafe.Pointer(der), derLength))
		C.free(unsafe.Pointer(der))
		if keyErr != nil {
			C.free(unsafe.Pointer(signature))
			return nil, "", keyErr
		}
		defer C.free(unsafe.Pointer(signature))
		return C.GoBytes(unsafe.Pointer(signature), signatureLength), key.credentialID, nil
	case C.UV_CANCELLED:
		if der != nil && derLength > 0 {
			C.free(unsafe.Pointer(der))
		}
		return nil, "", darwinUserVerificationCancelled(UserVerificationCancellationInterrupted, "the verification operation is no longer active")
	case C.UV_MISSING:
		if der != nil && derLength > 0 {
			C.free(unsafe.Pointer(der))
		}
		return nil, "", darwinUserVerificationUnavailable(UserVerificationUnavailableCredentialMissing, darwinUserVerificationCredentialMissingMessage)
	case C.UV_INVALID_PROTECTION:
		if der != nil && derLength > 0 {
			C.free(unsafe.Pointer(der))
		}
		return nil, "", darwinUserVerificationUnavailable(UserVerificationUnavailableProviderUnavailable, darwinUserVerificationInvalidProtectionMessage)
	default:
		if der != nil && derLength > 0 {
			C.free(unsafe.Pointer(der))
		}
		return nil, "", classifyDarwinNativeError(native)
	}
}

func classifyDarwinNativeError(native C.uv_error) *darwinUserVerificationError {
	domain := "NSOSStatusErrorDomain"
	if native.domain == C.UV_DOMAIN_LOCAL_AUTHENTICATION {
		domain = "com.apple.LocalAuthentication"
	}
	return classifyDarwinUserVerificationError(domain, int64(native.code))
}
