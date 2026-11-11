#ifndef WORKSHELF_KEYCHAIN_H
#define WORKSHELF_KEYCHAIN_H
#include <stddef.h>
int workshelf_secret_create(const char *service, const char *reference, const void *data, size_t length);
int workshelf_secret_read(const char *service, const char *reference, void **data, size_t *length);
int workshelf_secret_delete(const char *service, const char *reference);
void workshelf_secret_free(void *data, size_t length);
#endif
