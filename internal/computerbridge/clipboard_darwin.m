//go:build darwin && cgo

#include <AppKit/AppKit.h>
#include <Foundation/Foundation.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static char* taiyiCopyErrorMessage(NSString* message) {
	if (message == nil) {
		message = @"未知错误";
	}
	const char* utf8 = [message UTF8String];
	if (utf8 == NULL) {
		utf8 = "未知错误";
	}
	char* copied = (char*)malloc(strlen(utf8) + 1);
	if (copied == NULL) {
		return NULL;
	}
	strcpy(copied, utf8);
	return copied;
}

static char* taiyiCopyNSErrorMessage(NSError* error) {
	if (error == nil) {
		return taiyiCopyErrorMessage(nil);
	}
	return taiyiCopyErrorMessage([error localizedDescription]);
}

static void taiyiSetErrorBytes(char* bytes, void** outErrBytes, long* outErrLen) {
	if (outErrBytes != NULL) {
		*outErrBytes = bytes;
	}
	if (outErrLen != NULL) {
		*outErrLen = bytes == NULL ? 0 : (long)strlen(bytes);
	}
}

int clipboardSnapshotImpl(void** outBytes, long* outLen, void** outErrBytes, long* outErrLen) {
	@autoreleasepool {
		NSPasteboard* pasteboard = [NSPasteboard generalPasteboard];
		NSArray* items = [pasteboard pasteboardItems];
		if (items == nil) {
			items = [NSArray array];
		}
		NSMutableArray* serializedItems = [NSMutableArray arrayWithCapacity:[items count]];
		for (NSPasteboardItem* item in items) {
			NSArray* types = [item types];
			NSMutableDictionary* entry = [NSMutableDictionary dictionaryWithCapacity:[types count]];
			for (NSString* type in types) {
				NSData* data = [item dataForType:type];
				if (data != nil) {
					[entry setObject:data forKey:type];
				}
			}
			[serializedItems addObject:entry];
		}
		NSError* error = nil;
		NSData* snapshot = [NSPropertyListSerialization dataWithPropertyList:serializedItems format:NSPropertyListBinaryFormat_v1_0 options:0 error:&error];
		if (snapshot == nil) {
			taiyiSetErrorBytes(taiyiCopyNSErrorMessage(error), outErrBytes, outErrLen);
			return 1;
		}
		NSUInteger length = [snapshot length];
		void* bytes = NULL;
		if (length > 0) {
			bytes = malloc(length);
			if (bytes == NULL) {
				taiyiSetErrorBytes(taiyiCopyErrorMessage(@"为剪贴板快照分配内存失败"), outErrBytes, outErrLen);
				return 1;
			}
			memcpy(bytes, [snapshot bytes], length);
		}
		if (outBytes != NULL) {
			*outBytes = bytes;
		}
		if (outLen != NULL) {
			*outLen = (long)length;
		}
		taiyiSetErrorBytes(NULL, outErrBytes, outErrLen);
		return 0;
	}
}

int clipboardRestoreImpl(const void* bytes, long length, void** outErrBytes, long* outErrLen) {
	@autoreleasepool {
		NSData* snapshot = [NSData dataWithBytes:bytes length:(NSUInteger)length];
		NSError* error = nil;
		NSPropertyListFormat format = NSPropertyListBinaryFormat_v1_0;
		id propertyList = [NSPropertyListSerialization propertyListWithData:snapshot options:NSPropertyListImmutable format:&format error:&error];
		if (propertyList == nil) {
			taiyiSetErrorBytes(taiyiCopyNSErrorMessage(error), outErrBytes, outErrLen);
			return 1;
		}
		if (![propertyList isKindOfClass:[NSArray class]]) {
			taiyiSetErrorBytes(taiyiCopyErrorMessage(@"剪贴板快照格式无效"), outErrBytes, outErrLen);
			return 1;
		}
		NSArray* serializedItems = (NSArray*)propertyList;
		NSPasteboard* pasteboard = [NSPasteboard generalPasteboard];
		[pasteboard clearContents];
		if ([serializedItems count] == 0) {
			taiyiSetErrorBytes(NULL, outErrBytes, outErrLen);
			return 0;
		}
		NSMutableArray* items = [NSMutableArray arrayWithCapacity:[serializedItems count]];
		for (id rawEntry in serializedItems) {
			if (![rawEntry isKindOfClass:[NSDictionary class]]) {
				taiyiSetErrorBytes(taiyiCopyErrorMessage(@"剪贴板条目格式无效"), outErrBytes, outErrLen);
				return 1;
			}
			NSDictionary* entry = (NSDictionary*)rawEntry;
			NSPasteboardItem* item = [[[NSPasteboardItem alloc] init] autorelease];
			for (id rawKey in entry) {
				if (![rawKey isKindOfClass:[NSString class]]) {
					continue;
				}
				id rawValue = [entry objectForKey:rawKey];
				if (![rawValue isKindOfClass:[NSData class]]) {
					continue;
				}
				[item setData:(NSData*)rawValue forType:(NSString*)rawKey];
			}
			[items addObject:item];
		}
		if (![pasteboard writeObjects:items]) {
			taiyiSetErrorBytes(taiyiCopyErrorMessage(@"恢复系统剪贴板失败"), outErrBytes, outErrLen);
			return 1;
		}
		taiyiSetErrorBytes(NULL, outErrBytes, outErrLen);
		return 0;
	}
}

int clipboardSetTextImpl(const uint16_t* chars, long length, void** outErrBytes, long* outErrLen) {
	@autoreleasepool {
		NSString* text = [[[NSString alloc] initWithCharacters:(const unichar*)chars length:(NSUInteger)length] autorelease];
		NSPasteboard* pasteboard = [NSPasteboard generalPasteboard];
		[pasteboard clearContents];
		if (![pasteboard setString:text forType:NSPasteboardTypeString]) {
			taiyiSetErrorBytes(taiyiCopyErrorMessage(@"写入系统剪贴板文本失败"), outErrBytes, outErrLen);
			return 1;
		}
		taiyiSetErrorBytes(NULL, outErrBytes, outErrLen);
		return 0;
	}
}