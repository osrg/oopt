# bash completion for oopt                                 -*- shell-script -*-

__oopt_debug()
{
    if [[ -n ${BASH_COMP_DEBUG_FILE} ]]; then
        echo "$*" >> "${BASH_COMP_DEBUG_FILE}"
    fi
}

# Homebrew on Macs have version 1.3 of bash-completion which doesn't include
# _init_completion. This is a very minimal version of that function.
__oopt_init_completion()
{
    COMPREPLY=()
    _get_comp_words_by_ref "$@" cur prev words cword
}

__oopt_index_of_word()
{
    local w word=$1
    shift
    index=0
    for w in "$@"; do
        [[ $w = "$word" ]] && return
        index=$((index+1))
    done
    index=-1
}

__oopt_contains_word()
{
    local w word=$1; shift
    for w in "$@"; do
        [[ $w = "$word" ]] && return
    done
    return 1
}

__oopt_handle_reply()
{
    __oopt_debug "${FUNCNAME[0]}"
    case $cur in
        -*)
            if [[ $(type -t compopt) = "builtin" ]]; then
                compopt -o nospace
            fi
            local allflags
            if [ ${#must_have_one_flag[@]} -ne 0 ]; then
                allflags=("${must_have_one_flag[@]}")
            else
                allflags=("${flags[*]} ${two_word_flags[*]}")
            fi
            COMPREPLY=( $(compgen -W "${allflags[*]}" -- "$cur") )
            if [[ $(type -t compopt) = "builtin" ]]; then
                [[ "${COMPREPLY[0]}" == *= ]] || compopt +o nospace
            fi

            # complete after --flag=abc
            if [[ $cur == *=* ]]; then
                if [[ $(type -t compopt) = "builtin" ]]; then
                    compopt +o nospace
                fi

                local index flag
                flag="${cur%=*}"
                __oopt_index_of_word "${flag}" "${flags_with_completion[@]}"
                COMPREPLY=()
                if [[ ${index} -ge 0 ]]; then
                    PREFIX=""
                    cur="${cur#*=}"
                    ${flags_completion[${index}]}
                    if [ -n "${ZSH_VERSION}" ]; then
                        # zsh completion needs --flag= prefix
                        eval "COMPREPLY=( \"\${COMPREPLY[@]/#/${flag}=}\" )"
                    fi
                fi
            fi
            return 0;
            ;;
    esac

    # check if we are handling a flag with special work handling
    local index
    __oopt_index_of_word "${prev}" "${flags_with_completion[@]}"
    if [[ ${index} -ge 0 ]]; then
        ${flags_completion[${index}]}
        return
    fi

    # we are parsing a flag and don't have a special handler, no completion
    if [[ ${cur} != "${words[cword]}" ]]; then
        return
    fi

    local completions
    completions=("${commands[@]}")
    if [[ ${#must_have_one_noun[@]} -ne 0 ]]; then
        completions=("${must_have_one_noun[@]}")
    fi
    if [[ ${#must_have_one_flag[@]} -ne 0 ]]; then
        completions+=("${must_have_one_flag[@]}")
    fi
    COMPREPLY=( $(compgen -W "${completions[*]}" -- "$cur") )

    if [[ ${#COMPREPLY[@]} -eq 0 && ${#noun_aliases[@]} -gt 0 && ${#must_have_one_noun[@]} -ne 0 ]]; then
        COMPREPLY=( $(compgen -W "${noun_aliases[*]}" -- "$cur") )
    fi

    if [[ ${#COMPREPLY[@]} -eq 0 ]]; then
        declare -F __custom_func >/dev/null && __custom_func
    fi

    # available in bash-completion >= 2, not always present on macOS
    if declare -F __ltrim_colon_completions >/dev/null; then
        __ltrim_colon_completions "$cur"
    fi
}

# The arguments should be in the form "ext1|ext2|extn"
__oopt_handle_filename_extension_flag()
{
    local ext="$1"
    _filedir "@(${ext})"
}

__oopt_handle_subdirs_in_dir_flag()
{
    local dir="$1"
    pushd "${dir}" >/dev/null 2>&1 && _filedir -d && popd >/dev/null 2>&1
}

__oopt_handle_flag()
{
    __oopt_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    # if a command required a flag, and we found it, unset must_have_one_flag()
    local flagname=${words[c]}
    local flagvalue
    # if the word contained an =
    if [[ ${words[c]} == *"="* ]]; then
        flagvalue=${flagname#*=} # take in as flagvalue after the =
        flagname=${flagname%=*} # strip everything after the =
        flagname="${flagname}=" # but put the = back
    fi
    __oopt_debug "${FUNCNAME[0]}: looking for ${flagname}"
    if __oopt_contains_word "${flagname}" "${must_have_one_flag[@]}"; then
        must_have_one_flag=()
    fi

    # if you set a flag which only applies to this command, don't show subcommands
    if __oopt_contains_word "${flagname}" "${local_nonpersistent_flags[@]}"; then
      commands=()
    fi

    # keep flag value with flagname as flaghash
    # flaghash variable is an associative array which is only supported in bash > 3.
    if [[ -z "${BASH_VERSION}" || "${BASH_VERSINFO[0]}" -gt 3 ]]; then
        if [ -n "${flagvalue}" ] ; then
            flaghash[${flagname}]=${flagvalue}
        elif [ -n "${words[ $((c+1)) ]}" ] ; then
            flaghash[${flagname}]=${words[ $((c+1)) ]}
        else
            flaghash[${flagname}]="true" # pad "true" for bool flag
        fi
    fi

    # skip the argument to a two word flag
    if __oopt_contains_word "${words[c]}" "${two_word_flags[@]}"; then
        c=$((c+1))
        # if we are looking for a flags value, don't show commands
        if [[ $c -eq $cword ]]; then
            commands=()
        fi
    fi

    c=$((c+1))

}

__oopt_handle_noun()
{
    __oopt_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    if __oopt_contains_word "${words[c]}" "${must_have_one_noun[@]}"; then
        must_have_one_noun=()
    elif __oopt_contains_word "${words[c]}" "${noun_aliases[@]}"; then
        must_have_one_noun=()
    fi

    nouns+=("${words[c]}")
    c=$((c+1))
}

__oopt_handle_command()
{
    __oopt_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"

    local next_command
    if [[ -n ${last_command} ]]; then
        next_command="_${last_command}_${words[c]//:/__}"
    else
        if [[ $c -eq 0 ]]; then
            next_command="_$(basename "${words[c]//:/__}")"
        else
            next_command="_${words[c]//:/__}"
        fi
    fi
    c=$((c+1))
    __oopt_debug "${FUNCNAME[0]}: looking for ${next_command}"
    declare -F "$next_command" >/dev/null && $next_command
}

__oopt_handle_word()
{
    if [[ $c -ge $cword ]]; then
        __oopt_handle_reply
        return
    fi
    __oopt_debug "${FUNCNAME[0]}: c is $c words[c] is ${words[c]}"
    if [[ "${words[c]}" == -* ]]; then
        __oopt_handle_flag
    elif __oopt_contains_word "${words[c]}" "${commands[@]}"; then
        __oopt_handle_command
    elif [[ $c -eq 0 ]] && __oopt_contains_word "$(basename "${words[c]}")" "${commands[@]}"; then
        __oopt_handle_command
    else
        __oopt_handle_noun
    fi
    __oopt_handle_word
}

_oopt_allow-oversubscription()
{
    last_command="oopt_allow-oversubscription"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("false")
    must_have_one_noun+=("true")
    noun_aliases=()
}

_oopt_commit()
{
    last_command="oopt_commit"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--message=")
    two_word_flags+=("-m")
    flags+=("--reboot")
    flags+=("-r")
    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_diff()
{
    last_command="oopt_diff"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_dump()
{
    last_command="oopt_dump"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--verbose")
    flags+=("-v")
    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_init()
{
    last_command="oopt_init"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--force")
    flags+=("-f")
    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__description_clear()
{
    last_command="oopt_interface__description_clear"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__description()
{
    last_command="oopt_interface__description"
    commands=()
    commands+=("clear")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__optical-module-connection_clear()
{
    last_command="oopt_interface__optical-module-connection_clear"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__optical-module-connection_id()
{
    last_command="oopt_interface__optical-module-connection_id"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__optical-module-connection_optical-module_channel()
{
    last_command="oopt_interface__optical-module-connection_optical-module_channel"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("A")
    must_have_one_noun+=("B")
    noun_aliases=()
}

_oopt_interface__optical-module-connection_optical-module_name()
{
    last_command="oopt_interface__optical-module-connection_optical-module_name"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__optical-module-connection_optical-module()
{
    last_command="oopt_interface__optical-module-connection_optical-module"
    commands=()
    commands+=("channel")
    commands+=("name")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__optical-module-connection()
{
    last_command="oopt_interface__optical-module-connection"
    commands=()
    commands+=("clear")
    commands+=("id")
    commands+=("optical-module")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface__state()
{
    last_command="oopt_interface__state"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface_()
{
    last_command="oopt_interface_"
    commands=()
    commands+=("description")
    commands+=("optical-module-connection")
    commands+=("state")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_interface()
{
    last_command="oopt_interface"
    commands=()
    commands+=("")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__allow-oversubscription_clear()
{
    last_command="oopt_optical-module__allow-oversubscription_clear"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__allow-oversubscription()
{
    last_command="oopt_optical-module__allow-oversubscription"
    commands=()
    commands+=("clear")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("false")
    must_have_one_noun+=("true")
    noun_aliases=()
}

_oopt_optical-module__ber-interval()
{
    last_command="oopt_optical-module__ber-interval"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__description_clear()
{
    last_command="oopt_optical-module__description_clear"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__description()
{
    last_command="oopt_optical-module__description"
    commands=()
    commands+=("clear")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__disable()
{
    last_command="oopt_optical-module__disable"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__enable()
{
    last_command="oopt_optical-module__enable"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__frequency_channel()
{
    last_command="oopt_optical-module__frequency_channel"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__frequency_grid()
{
    last_command="oopt_optical-module__frequency_grid"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("GRID_100GHZ")
    must_have_one_noun+=("GRID_25GHZ")
    must_have_one_noun+=("GRID_33GHZ")
    must_have_one_noun+=("GRID_50GHZ")
    noun_aliases=()
}

_oopt_optical-module__frequency()
{
    last_command="oopt_optical-module__frequency"
    commands=()
    commands+=("channel")
    commands+=("grid")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module__losi()
{
    last_command="oopt_optical-module__losi"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("off")
    must_have_one_noun+=("on")
    noun_aliases=()
}

_oopt_optical-module__modulation-type()
{
    last_command="oopt_optical-module__modulation-type"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("DP_16QAM")
    must_have_one_noun+=("DP_QPSK")
    noun_aliases=()
}

_oopt_optical-module__prbs()
{
    last_command="oopt_optical-module__prbs"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("off")
    must_have_one_noun+=("on")
    noun_aliases=()
}

_oopt_optical-module__state()
{
    last_command="oopt_optical-module__state"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module_()
{
    last_command="oopt_optical-module_"
    commands=()
    commands+=("allow-oversubscription")
    commands+=("ber-interval")
    commands+=("description")
    commands+=("disable")
    commands+=("enable")
    commands+=("frequency")
    commands+=("losi")
    commands+=("modulation-type")
    commands+=("prbs")
    commands+=("state")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--verbose")
    flags+=("-v")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_optical-module()
{
    last_command="oopt_optical-module"
    commands=()
    commands+=("")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--verbose")
    flags+=("-v")
    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_port__breakout-mode_channel-speed()
{
    last_command="oopt_port__breakout-mode_channel-speed"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("SPEED_100GB")
    must_have_one_noun+=("SPEED_100MB")
    must_have_one_noun+=("SPEED_10GB")
    must_have_one_noun+=("SPEED_10MB")
    must_have_one_noun+=("SPEED_1GB")
    must_have_one_noun+=("SPEED_2500MB")
    must_have_one_noun+=("SPEED_25GB")
    must_have_one_noun+=("SPEED_40GB")
    must_have_one_noun+=("SPEED_50GB")
    must_have_one_noun+=("SPEED_5GB")
    must_have_one_noun+=("SPEED_UNKNOWN")
    noun_aliases=()
}

_oopt_port__breakout-mode_num-channels()
{
    last_command="oopt_port__breakout-mode_num-channels"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    must_have_one_noun+=("1")
    must_have_one_noun+=("4")
    noun_aliases=()
}

_oopt_port__breakout-mode()
{
    last_command="oopt_port__breakout-mode"
    commands=()
    commands+=("channel-speed")
    commands+=("num-channels")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_port__description_clear()
{
    last_command="oopt_port__description_clear"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_port__description()
{
    last_command="oopt_port__description"
    commands=()
    commands+=("clear")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_port_()
{
    last_command="oopt_port_"
    commands=()
    commands+=("breakout-mode")
    commands+=("description")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_port()
{
    last_command="oopt_port"
    commands=()
    commands+=("")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_reboot()
{
    last_command="oopt_reboot"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_rollback()
{
    last_command="oopt_rollback"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--message=")
    two_word_flags+=("-m")
    flags+=("--number=")
    two_word_flags+=("-n")
    flags+=("--reboot")
    flags+=("-r")
    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_status()
{
    last_command="oopt_status"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt_stop()
{
    last_command="oopt_stop"
    commands=()

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

_oopt()
{
    last_command="oopt"
    commands=()
    commands+=("allow-oversubscription")
    commands+=("commit")
    commands+=("diff")
    commands+=("dump")
    commands+=("init")
    commands+=("interface")
    commands+=("optical-module")
    commands+=("port")
    commands+=("reboot")
    commands+=("rollback")
    commands+=("status")
    commands+=("stop")

    flags=()
    two_word_flags=()
    local_nonpersistent_flags=()
    flags_with_completion=()
    flags_completion=()

    flags+=("--dry")
    flags+=("-d")
    flags+=("--git-dir=")
    two_word_flags+=("-c")
    flags+=("--virtual")

    must_have_one_flag=()
    must_have_one_noun=()
    noun_aliases=()
}

__start_oopt()
{
    local cur prev words cword
    declare -A flaghash 2>/dev/null || :
    if declare -F _init_completion >/dev/null 2>&1; then
        _init_completion -s || return
    else
        __oopt_init_completion -n "=" || return
    fi

    local c=0
    local flags=()
    local two_word_flags=()
    local local_nonpersistent_flags=()
    local flags_with_completion=()
    local flags_completion=()
    local commands=("oopt")
    local must_have_one_flag=()
    local must_have_one_noun=()
    local last_command
    local nouns=()

    __oopt_handle_word
}

if [[ $(type -t compopt) = "builtin" ]]; then
    complete -o default -F __start_oopt oopt
else
    complete -o default -o nospace -F __start_oopt oopt
fi

# ex: ts=4 sw=4 et filetype=sh
