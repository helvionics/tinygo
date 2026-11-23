MEMORY
{
    FLASH_TEXT (rx) : ORIGIN = 0x00000000, LENGTH = 256K
    RAM (rwx)       : ORIGIN = 0x20000000, LENGTH = 64K
}

_stack_size = 2K;

INCLUDE "targets/arm.ld"
