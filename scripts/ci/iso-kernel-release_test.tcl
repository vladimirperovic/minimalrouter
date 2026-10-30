#!/usr/bin/env tclsh
# Exercise the actual Expect pattern with Tcl's same regexp engine and the
# production kernel policy. No QEMU, guest disk or copied parser is involved.
set script_dir [file dirname [file normalize [info script]]]
set handle [open [file join $script_dir iso-full-install.exp] r]
set source [read $handle]
close $handle
set matches [regexp -all -inline {(?m)^[ \t]*-re (\{RUNNING_KERNEL_RELEASE=[^\n]*\}) \{$} $source]
if {[llength $matches] != 2} {
    error "expected exactly one literal installed-kernel pattern in iso-full-install.exp"
}
set pattern [lindex [lindex $matches 1] 0]
set policy [file normalize [file join $script_dir .. .. packaging alpine check-kernel-security.sh]]

proc check_capture {pattern buffer expected release context} {
    set matched [regexp $pattern $buffer _ captured]
    if {$matched != $expected} {
        error "$context: expected match=$expected, got $matched"
    }
    if {$matched && $captured ne $release} {
        error "$context: captured a partial or contaminated kernel release"
    }
}

proc check_policy {policy release expected context} {
    set rejected [catch {exec sh $policy --release $release} output]
    set accepted [expr {!$rejected}]
    if {$accepted != $expected} {
        error "$context: expected accepted=$expected, got $accepted"
    }
    if {!$rejected && $output ne $release} {
        error "$context: policy did not return the exact captured kernel release"
    }
}

set records 0
set deliveries 0
foreach {ending_name ending} [list LF "\n" CRLF "\r\n" CRCRLF "\r\r\n" CR "\r"] {
    foreach {release accepted} {
        6.18.56-0-lts 1
        6.18.59-0-lts 1
        6.18.55-0-lts 0
        6.18.059-0-lts 0
        6.19.59-0-lts 0
        6.18.59-0-virt 0
        6.18.59-0-lts-extra 0
        {6.18.59-0-lts garbage} 0
        {6.18.59-0-lts } 0
        not-a-kernel 0
    } {
        set prefix "boot noise\r\nlocalhost:~# RUNNING_KERNEL_RELEASE=$release"
        set record "${prefix}${ending}localhost:~# "
        # Deliver the record in two pieces at every possible boundary. A
        # release-looking prefix must never match before its first delimiter,
        # including when the second piece extends it with a malformed suffix.
        for {set split 0} {$split <= [string length $record]} {incr split} {
            set buffer [string range $record 0 [expr {$split - 1}]]
            set complete [expr {$split > [string length $prefix]}]
            check_capture $pattern $buffer $complete $release "$ending_name split $split first"
            append buffer [string range $record $split end]
            check_capture $pattern $buffer 1 $release "$ending_name split $split complete"
            incr deliveries
        }
        regexp $pattern $record _ captured
        check_policy $policy $captured $accepted "$ending_name $release"
        incr records
    }
}

foreach incomplete [list "" "RUNNING_KERNEL_RELEASE=" "RUNNING_KERNEL_RELEASE=\r\n" \
        "RUNNING_KERNEL_RELEASE=6.18.59-0-lts" "OTHER_KERNEL_RELEASE=6.18.59-0-lts\r\n"] {
    check_capture $pattern $incomplete 0 "" "missing or incomplete record"
}

# TTY echo remains disabled in the E2E. If an echoed command nevertheless
# reaches the parser, its printf format must fail policy, never turn green.
set echoed {printf 'RUNNING_KERNEL_RELEASE=%s\n' "$(uname -r)"}
append echoed "\r\r\n"
if {![regexp $pattern $echoed _ captured]} {
    error "echo fixture did not exercise the policy rejection path"
}
check_policy $policy $captured 0 "echoed printf command"

puts "ISO kernel-release parser: $records records and $deliveries chunk boundaries passed; unsafe releases and command echo rejected"
