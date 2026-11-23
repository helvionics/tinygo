.syntax unified
.section .isr_vector, "a", %progbits
.global __isr_vector
__isr_vector:
    .long _stack_top
    .long Reset_Handler
